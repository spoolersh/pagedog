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
	"strings"
	"text/template"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/gobwas/flagutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/spoolersh/pagedog"
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

// workerCommand takes incidents off the input queue, investigates each, and
// puts the report on the output queue. The server runs it, one per incident
// in flight, with the flags that mirror its configuration; the Spooler API
// key comes from the environment alone.
type workerCommand struct {
	logLevel slog.Level

	spoolerHost        string
	spoolerNoTLS       bool
	spoolerSpool       string
	spoolerQueueInput  string
	spoolerQueueOutput string

	// The report store is chosen after the flags are parsed.
	reportStoreKind string
	fsReportStore   pagedog_fs.ReportStore

	// The model is chosen after the flags are parsed.
	agentModelKind  string
	agentMaxTurns   int
	claudeModelName string
	claudeMaxTokens int
	openaiModelName string
	openaiMaxTokens int
	geminiModelName string
	geminiMaxTokens int

	// The task is a template over the incident; empty means the built-in.
	agentTask string

	// The tools are chosen after the flags are parsed.
	agentTools    []string
	prometheusURL string
	lokiURL       string
	mcpServers    []MCPServer
}

func (c *workerCommand) Summary() string {
	return "Takes incidents off the queue, investigates each, and reports."
}

func (c *workerCommand) Epilog() string {
	return "The Spooler API key is read from " + workerSpoolerAPIKey + ", never from a flag."
}

func (c *workerCommand) DefineFlags(fs *flag.FlagSet) {
	fs.Var(LogLevelVar(&c.logLevel),
		"log-level",
		"log level",
	)
	flagutil.Subset(fs, "spooler", func(fs *flag.FlagSet) {
		fs.StringVar(&c.spoolerHost,
			"host", "127.0.0.1:8080",
			"spooler api `host:port`",
		)
		fs.BoolVar(&c.spoolerNoTLS,
			"no-tls", true,
			"do not use tls encryption (local runs only!)",
		)
		fs.StringVar(&c.spoolerSpool,
			"spool", "default",
			"spool name",
		)
		fs.StringVar(&c.spoolerQueueInput,
			"input", "incidents",
			"input queue name",
		)
		fs.StringVar(&c.spoolerQueueOutput,
			"output", "reports",
			"output queue name",
		)
	})
	flagutil.Subset(fs, "report", func(fs *flag.FlagSet) {
		fs.StringVar(&c.reportStoreKind,
			"store", "fs",
			"where the reports go: fs",
		)
		flagutil.Subset(fs, "fs", func(fs *flag.FlagSet) {
			fs.StringVar(&c.fsReportStore.Dir,
				"dir", "reports",
				"directory the reports go in, one file per incident",
			)
		})
	})
	flagutil.Subset(fs, "agent", func(fs *flag.FlagSet) {
		fs.StringVar(&c.agentModelKind,
			"model", "claude",
			"the model to investigate with: claude, openai, gemini",
		)
		fs.IntVar(&c.agentMaxTurns,
			"max-turns", ai.DefaultMaxTurns,
			"how many times the model is asked per incident before giving up",
		)
		fs.StringVar(&c.agentTask,
			"task", "",
			"the task given to the model, a Go text/template over the incident; empty means the built-in",
		)
		fs.Var(StringsVar(&c.agentTools),
			"tools",
			"what the model may look at, comma-separated: prometheus, loki, mcp",
		)
		flagutil.Subset(fs, "prometheus", func(fs *flag.FlagSet) {
			fs.StringVar(&c.prometheusURL,
				"url", "",
				"prometheus server `url`",
			)
		})
		flagutil.Subset(fs, "loki", func(fs *flag.FlagSet) {
			fs.StringVar(&c.lokiURL,
				"url", "",
				"loki server `url`",
			)
		})
		flagutil.Subset(fs, "mcp", func(fs *flag.FlagSet) {
			fs.Var(MCPServersVar(&c.mcpServers),
				"server",
				"an mcp server as `json`: {name, url with headers or command with args and env, tools}; one per flag",
			)
		})
		flagutil.Subset(fs, "claude", func(fs *flag.FlagSet) {
			fs.StringVar(&c.claudeModelName,
				"name", string(claude.DefaultModel),
				"model `name`",
			)
			fs.IntVar(&c.claudeMaxTokens,
				"max-tokens", claude.DefaultMaxTokens,
				"most tokens one completion may produce",
			)
		})
		flagutil.Subset(fs, "openai", func(fs *flag.FlagSet) {
			fs.StringVar(&c.openaiModelName,
				"name", pagedog_openai.DefaultModel,
				"model `name`",
			)
			fs.IntVar(&c.openaiMaxTokens,
				"max-tokens", pagedog_openai.DefaultMaxTokens,
				"most tokens one completion may produce",
			)
		})
		flagutil.Subset(fs, "gemini", func(fs *flag.FlagSet) {
			fs.StringVar(&c.geminiModelName,
				"name", gemini.DefaultModel,
				"model `name`",
			)
			fs.IntVar(&c.geminiMaxTokens,
				"max-tokens", gemini.DefaultMaxTokens,
				"most tokens one completion may produce",
			)
		})
	})
}

func (c *workerCommand) Run(ctx context.Context, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: c.logLevel,
	}))

	spoolerClient := spooler.Client{
		Trace: spooler.LoggerClientTrace(logger.With(
			"component", "spooler",
		)),
		Host: c.spoolerHost,
		// A secret: it comes from the environment, never from a flag.
		// Unset, it is the local server's, which takes any.
		APIKey:             cmp.Or(os.Getenv(workerSpoolerAPIKey), "local"),
		InsecureDisableTLS: c.spoolerNoTLS,
	}

	task, err := template.New("task").Parse(cmp.Or(c.agentTask, defaultTask))
	if err != nil {
		return fmt.Errorf(
			"task: %w",
			err,
		)
	}
	// A field the template names wrongly fails here, once, not per incident.
	if err := task.Execute(io.Discard, taskData{}); err != nil {
		return fmt.Errorf(
			"task: %w",
			err,
		)
	}
	var reportStore pagedog.ReportStore
	switch c.reportStoreKind {
	case "fs":
		c.fsReportStore.Trace = pagedog_fs.LoggerTrace(logger.With(
			"component", "report",
		))
		reportStore = &c.fsReportStore
	default:
		return fmt.Errorf(
			"unknown report store: %q",
			c.reportStoreKind,
		)
	}

	var model ai.Model
	switch c.agentModelKind {
	case "claude":
		model = &claude.Model{
			Client:    anthropic.NewClient(),
			Name:      anthropic.Model(c.claudeModelName),
			MaxTokens: c.claudeMaxTokens,
		}
	case "openai":
		model = &pagedog_openai.Model{
			Client:    openai.NewClient(),
			Name:      c.openaiModelName,
			MaxTokens: c.openaiMaxTokens,
		}
	case "gemini":
		client, err := genai.NewClient(ctx, &genai.ClientConfig{})
		if err != nil {
			return err
		}
		model = &gemini.Model{
			Client:    client,
			Name:      c.geminiModelName,
			MaxTokens: c.geminiMaxTokens,
		}
	default:
		return fmt.Errorf(
			"unknown agent model: %q",
			c.agentModelKind,
		)
	}

	agent := ai.Agent{
		Trace: ai.LoggerAgentTrace(logger.With(
			"component", "agent",
		)),
		Model: model,
	}
	for _, kind := range c.agentTools {
		var tools []ai.Tool
		switch kind {
		case "prometheus":
			if c.prometheusURL == "" {
				return errors.New("prometheus: no url")
			}
			src := &prometheus.Client{
				URL: c.prometheusURL,
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
			if c.lokiURL == "" {
				return errors.New("loki: no url")
			}
			src := &loki.Client{
				URL: c.lokiURL,
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
			if len(c.mcpServers) == 0 {
				return errors.New("mcp: no servers")
			}
			for _, s := range c.mcpServers {
				var transport mcp.Transport
				switch {
				case s.URL != "":
					// A secret header is read from this process's
					// environment: the server never puts one on a
					// command line.
					header := make(http.Header, len(s.Headers))
					for name, h := range s.Headers {
						v := h.Value
						if h.Env != "" {
							var has bool
							if v, has = os.LookupEnv(h.Env); !has {
								return fmt.Errorf(
									"mcp server %q: header %s: %s is not set",
									s.Name, name, h.Env,
								)
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
					cmd := exec.Command(s.Command, s.Args...)
					// The server gets the variables named for it, and
					// nothing else of this process's.
					cmd.Env = make([]string, 0, len(s.Env))
					for _, name := range s.Env {
						if v, has := os.LookupEnv(name); has {
							cmd.Env = append(cmd.Env, name+"="+v)
						}
					}
					cmd.Stderr = os.Stderr
					transport = &mcp.CommandTransport{
						Command: cmd,
					}
				default:
					return fmt.Errorf(
						"mcp server %q: no url and no command",
						s.Name,
					)
				}
				client := mcp.NewClient(&mcp.Implementation{
					Name:    "pagedog-worker",
					Version: "0",
				}, nil)
				session, err := client.Connect(ctx, transport, nil)
				if err != nil {
					return fmt.Errorf(
						"mcp server %q: %w",
						s.Name, err,
					)
				}
				// The session lasts as long as this process; a child
				// process server is stopped with it.
				defer session.Close()
				ts, err := mcp_tools.Tools(ctx, session, s.Tools)
				if err != nil {
					return fmt.Errorf(
						"mcp server %q: %w",
						s.Name, err,
					)
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
			return fmt.Errorf(
				"unknown agent tool: %q",
				kind,
			)
		}
		for _, t := range tools {
			if err := agent.RegisterTool(t); err != nil {
				return err
			}
		}
	}

	pipe := &pagedog_spooler.Pipe[pagedog.Incident, pagedog.IncidentReport]{
		Client: &spoolerClient,
		Spool:  c.spoolerSpool,
		Input:  c.spoolerQueueInput,
		Output: c.spoolerQueueOutput,

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
			MaxTurns: c.agentMaxTurns,
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
		// A signal: the runner reports it.
		return nil
	}
	return err
}
