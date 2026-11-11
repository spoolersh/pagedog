// Worker takes incidents off the input queue, investigates each, and puts
// the report on the output queue.
package main

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/gobwas/flagutil"
	"github.com/gobwas/flagutil/parse/env"
	"github.com/gobwas/flagutil/parse/pargs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/pagedog/cmd"
	"github.com/spoolersh/spooler-go"
	"google.golang.org/genai"

	"github.com/spoolersh/pagedog/internal/ai"
	loki_tools "github.com/spoolersh/pagedog/internal/ai/tools/loki"
	mcp_tools "github.com/spoolersh/pagedog/internal/ai/tools/mcp"
	prometheus_tools "github.com/spoolersh/pagedog/internal/ai/tools/prometheus"
	"github.com/spoolersh/pagedog/internal/claude"
	pagedog_fs "github.com/spoolersh/pagedog/internal/fs"
	"github.com/spoolersh/pagedog/internal/gemini"
	"github.com/spoolersh/pagedog/internal/httputil"
	"github.com/spoolersh/pagedog/internal/loki"
	pagedog_openai "github.com/spoolersh/pagedog/internal/openai"
	"github.com/spoolersh/pagedog/internal/prometheus"
	pagedog_spooler "github.com/spoolersh/pagedog/internal/spooler"
)

// defaultTask is the task given to the model when no other is: a template
// over [taskData].
//
//go:embed task.md
var defaultTask string

// taskData is what the task template is executed with.
type taskData struct {
	// Incident is the incident, as the queue carried it.
	Incident pagedog.Incident

	// Raw is the incident as its source reported it, as text.
	Raw string

	// StartedAt is when the incident began and Now when the investigation
	// does, both RFC 3339.
	StartedAt string
	Now       string
}

func main() {
	signals := []os.Signal{
		syscall.SIGINT,
		syscall.SIGTERM,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx, stop := signal.NotifyContext(ctx, signals...)
	defer stop()

	var (
		logLevel = slog.LevelInfo

		spoolerClient = spooler.Client{
			Host:               "127.0.0.1:8080",
			APIKey:             "local",
			InsecureDisableTLS: true,
		}
		spoolerSpool       = "default"
		spoolerQueueInput  = "incidents"
		spoolerQueueOutput = "reports"

		// The report store is chosen after the flags are parsed.
		reportStoreKind = "fs"
		fsReportStore   = pagedog_fs.ReportStore{
			Dir: "reports",
		}

		// The model is chosen after the flags are parsed.
		agentModelKind  = "claude"
		agentMaxTurns   = ai.DefaultMaxTurns
		claudeModelName = string(claude.DefaultModel)
		claudeMaxTokens = claude.DefaultMaxTokens
		openaiModelName = pagedog_openai.DefaultModel
		openaiMaxTokens = pagedog_openai.DefaultMaxTokens
		geminiModelName = gemini.DefaultModel
		geminiMaxTokens = gemini.DefaultMaxTokens

		// The task is a template over the incident; empty means the
		// built-in.
		agentTask string

		// The tools are chosen after the flags are parsed.
		agentTools    []string
		prometheusURL string
		lokiURL       string
		mcpServers    []cmd.MCPServer
	)
	fs := flag.NewFlagSet("pagedog_worker", flag.ExitOnError)
	fs.Var(cmd.LogLevelVar(&logLevel),
		"log-level",
		"log level",
	)
	flagutil.Subset(fs, "spooler", func(fs *flag.FlagSet) {
		fs.StringVar(&spoolerClient.Host,
			"host", spoolerClient.Host,
			"spooler api `host:port`",
		)
		fs.StringVar(&spoolerClient.APIKey,
			"api-key", spoolerClient.APIKey,
			"spooler api key",
		)
		fs.BoolVar(&spoolerClient.InsecureDisableTLS,
			"no-tls", spoolerClient.InsecureDisableTLS,
			"do not use tls encryption (local runs only!)",
		)
		fs.StringVar(&spoolerSpool,
			"spool", spoolerSpool,
			"spool name",
		)
		fs.StringVar(&spoolerQueueInput,
			"input", spoolerQueueInput,
			"input queue name",
		)
		fs.StringVar(&spoolerQueueOutput,
			"output", spoolerQueueOutput,
			"output queue name",
		)
	})
	flagutil.Subset(fs, "report", func(fs *flag.FlagSet) {
		fs.StringVar(&reportStoreKind,
			"store", reportStoreKind,
			"where the reports go: fs",
		)
		flagutil.Subset(fs, "fs", func(fs *flag.FlagSet) {
			fs.StringVar(&fsReportStore.Dir,
				"dir", fsReportStore.Dir,
				"directory the reports go in, one file per incident",
			)
		})
	})
	flagutil.Subset(fs, "agent", func(fs *flag.FlagSet) {
		fs.StringVar(&agentModelKind,
			"model", agentModelKind,
			"the model to investigate with: claude, openai, gemini",
		)
		fs.IntVar(&agentMaxTurns,
			"max-turns", agentMaxTurns,
			"how many times the model is asked per incident before giving up",
		)
		fs.StringVar(&agentTask,
			"task", agentTask,
			"the task given to the model, a Go text/template over the incident; empty means the built-in",
		)
		fs.Var(cmd.StringsVar(&agentTools),
			"tools",
			"what the model may look at, comma-separated: prometheus, loki, mcp",
		)
		flagutil.Subset(fs, "prometheus", func(fs *flag.FlagSet) {
			fs.StringVar(&prometheusURL,
				"url", prometheusURL,
				"prometheus server `url`",
			)
		})
		flagutil.Subset(fs, "loki", func(fs *flag.FlagSet) {
			fs.StringVar(&lokiURL,
				"url", lokiURL,
				"loki server `url`",
			)
		})
		flagutil.Subset(fs, "mcp", func(fs *flag.FlagSet) {
			fs.Var(cmd.MCPServersVar(&mcpServers),
				"server",
				"an mcp server as `json`: {name, url with headers or command with args and env, tools}; one per flag",
			)
		})
		flagutil.Subset(fs, "claude", func(fs *flag.FlagSet) {
			fs.StringVar(&claudeModelName,
				"name", claudeModelName,
				"model `name`",
			)
			fs.IntVar(&claudeMaxTokens,
				"max-tokens", claudeMaxTokens,
				"most tokens one completion may produce",
			)
		})
		flagutil.Subset(fs, "openai", func(fs *flag.FlagSet) {
			fs.StringVar(&openaiModelName,
				"name", openaiModelName,
				"model `name`",
			)
			fs.IntVar(&openaiMaxTokens,
				"max-tokens", openaiMaxTokens,
				"most tokens one completion may produce",
			)
		})
		flagutil.Subset(fs, "gemini", func(fs *flag.FlagSet) {
			fs.StringVar(&geminiModelName,
				"name", geminiModelName,
				"model `name`",
			)
			fs.IntVar(&geminiMaxTokens,
				"max-tokens", geminiMaxTokens,
				"most tokens one completion may produce",
			)
		})
	})
	_ = flagutil.Parse(ctx, fs,
		flagutil.WithCustomUsage(),
		flagutil.WithParser(
			&pargs.Parser{
				Args: os.Args[1:],
			},
			flagutil.WithNameFunc(func(name string) []string {
				switch name {
				case "spooler.api-key":
					return nil
				default:
					return []string{name}
				}
			}),
		),
		flagutil.WithParser(
			&env.Parser{
				Prefix:       "PAGEDOG_",
				SetSeparator: "_",
			},
			flagutil.WithNameFunc(func(name string) []string {
				switch name {
				case "spooler.api-key":
					return []string{name}
				default:
					return nil
				}
			}),
		),
	)

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	spoolerClient.Trace = spooler.LoggerClientTrace(logger.With(
		"component", "spooler",
	))

	task, err := template.New("task").Parse(cmp.Or(agentTask, defaultTask))
	if err != nil {
		fail(fmt.Errorf(
			"task: %w",
			err,
		))
	}
	// A field the template names wrongly fails here, once, not per incident.
	if err := task.Execute(io.Discard, taskData{}); err != nil {
		fail(fmt.Errorf(
			"task: %w",
			err,
		))
	}
	var reportStore pagedog.ReportStore
	switch reportStoreKind {
	case "fs":
		fsReportStore.Trace = pagedog_fs.LoggerTrace(logger.With(
			"component", "report",
		))
		reportStore = &fsReportStore
	default:
		fail(fmt.Errorf(
			"unknown report store: %q",
			reportStoreKind,
		))
	}

	var model ai.Model
	switch agentModelKind {
	case "claude":
		model = &claude.Model{
			Client:    anthropic.NewClient(),
			Name:      anthropic.Model(claudeModelName),
			MaxTokens: claudeMaxTokens,
		}
	case "openai":
		model = &pagedog_openai.Model{
			Client:    openai.NewClient(),
			Name:      openaiModelName,
			MaxTokens: openaiMaxTokens,
		}
	case "gemini":
		client, err := genai.NewClient(ctx, &genai.ClientConfig{})
		if err != nil {
			fail(err)
		}
		model = &gemini.Model{
			Client:    client,
			Name:      geminiModelName,
			MaxTokens: geminiMaxTokens,
		}
	default:
		fail(fmt.Errorf(
			"unknown agent model: %q",
			agentModelKind,
		))
	}

	agent := ai.Agent{
		Trace: ai.LoggerAgentTrace(logger.With(
			"component", "agent",
		)),
		Model: model,
	}
	for _, kind := range agentTools {
		var tools []ai.Tool
		switch kind {
		case "prometheus":
			if prometheusURL == "" {
				fail(errors.New("prometheus: no url"))
			}
			src := &prometheus.Client{
				URL: prometheusURL,
			}
			tools = []ai.Tool{
				&prometheus_tools.RangeQueryTool{
					Source: src,
				},
				&prometheus_tools.MetricNamesTool{
					Source: src,
				},
			}
		case "loki":
			if lokiURL == "" {
				fail(errors.New("loki: no url"))
			}
			src := &loki.Client{
				URL: lokiURL,
			}
			tools = []ai.Tool{
				&loki_tools.RangeQueryTool{
					Source: src,
				},
				&loki_tools.LabelsTool{
					Source: src,
				},
			}
		case "mcp":
			if len(mcpServers) == 0 {
				fail(errors.New("mcp: no servers"))
			}
			for _, s := range mcpServers {
				var transport mcp.Transport
				switch {
				case s.URL != "":
					// A secret header is read from this process's
					// environment: the launcher never puts one on a
					// command line.
					header := make(http.Header, len(s.Headers))
					for name, h := range s.Headers {
						v := h.Value
						if h.Env != "" {
							var has bool
							if v, has = os.LookupEnv(h.Env); !has {
								fail(fmt.Errorf(
									"mcp server %q: header %s: %s is not set",
									s.Name, name, h.Env,
								))
							}
						}
						header.Set(name, v)
					}
					transport = &mcp.StreamableClientTransport{
						Endpoint: s.URL,
						HTTPClient: &http.Client{
							Transport: &httputil.HeaderTransport{
								Header: header,
							},
						},
					}
				case s.Command != "":
					c := exec.Command(s.Command, s.Args...)
					// The server gets the variables named for it, and
					// nothing else of this process's.
					c.Env = make([]string, 0, len(s.Env))
					for _, name := range s.Env {
						if v, has := os.LookupEnv(name); has {
							c.Env = append(c.Env, name+"="+v)
						}
					}
					c.Stderr = os.Stderr
					transport = &mcp.CommandTransport{
						Command: c,
					}
				default:
					fail(fmt.Errorf(
						"mcp server %q: no url and no command",
						s.Name,
					))
				}
				client := mcp.NewClient(&mcp.Implementation{
					Name:    "pagedog-worker",
					Version: "0",
				}, nil)
				session, err := client.Connect(ctx, transport, nil)
				if err != nil {
					fail(fmt.Errorf(
						"mcp server %q: %w",
						s.Name, err,
					))
				}
				// The session lasts as long as this process; a child
				// process server is stopped with it.
				defer session.Close()
				ts, err := mcp_tools.Tools(ctx, session, s.Tools)
				if err != nil {
					fail(fmt.Errorf(
						"mcp server %q: %w",
						s.Name, err,
					))
				}
				names := make([]string, len(ts))
				for i, t := range ts {
					names[i] = t.Spec().Name
				}
				logger.Info("connected to mcp server",
					"name", s.Name,
					"tools", names,
				)
				tools = append(tools, ts...)
			}
		default:
			fail(fmt.Errorf(
				"unknown agent tool: %q",
				kind,
			))
		}
		for _, t := range tools {
			if err := agent.RegisterTool(t); err != nil {
				fail(err)
			}
		}
	}

	// Spool and queue are set after the flags are parsed.
	pipe := &pagedog_spooler.Pipe[pagedog.Incident, pagedog.IncidentReport]{
		Client: &spoolerClient,
		Spool:  spoolerSpool,
		Input:  spoolerQueueInput,
		Output: spoolerQueueOutput,

		// One report per incident: a send repeated on an unknown result
		// is one message on the reports queue.
		Dedup: func(r pagedog.IncidentReport) string {
			return r.Incident.Key
		},
	}
	err = pipe.Pump(ctx, func(sub context.Context, inc pagedog.Incident) (r pagedog.IncidentReport, err error) {
		var (
			now       = time.Now().UTC()
			startedAt = cmp.Or(inc.StartedAt, now)
			sb        strings.Builder
		)
		err = task.Execute(&sb, taskData{
			Incident:  inc,
			Raw:       string(inc.Raw),
			StartedAt: startedAt.Format(time.RFC3339),
			Now:       now.Format(time.RFC3339),
		})
		if err != nil {
			return r, err
		}

		res, err := agent.Do(sub, ai.DoRequest{
			Task:     sb.String(),
			MaxTurns: agentMaxTurns,
		})
		if err != nil {
			return r, err
		}
		u, err := reportStore.Store(sub, inc, pagedog.Report{
			Text: res.Output,
		})
		if err != nil {
			return r, err
		}
		return pagedog.IncidentReport{
			Incident:  inc,
			ReportURI: u,
		}, nil
	})
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "fatal error: %v\n", err)
	os.Exit(1)
}
