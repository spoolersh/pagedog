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
	"strings"
	"text/template"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	anthropic_option "github.com/anthropics/anthropic-sdk-go/v2/option"
	google_github "github.com/google/go-github/v93/github"
	"github.com/openai/openai-go/v3"
	openai_option "github.com/openai/openai-go/v3/option"
	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/spooler-go"
	"google.golang.org/genai"

	"github.com/spoolersh/pagedog/internal/ai"
	"github.com/spoolersh/pagedog/internal/claude"
	pagedog_config "github.com/spoolersh/pagedog/internal/config"
	pagedog_fs "github.com/spoolersh/pagedog/internal/fs"
	"github.com/spoolersh/pagedog/internal/gemini"
	pagedog_github "github.com/spoolersh/pagedog/internal/github"
	"github.com/spoolersh/pagedog/internal/loki"
	pagedog_mcp "github.com/spoolersh/pagedog/internal/mcp"
	pagedog_openai "github.com/spoolersh/pagedog/internal/openai"
	"github.com/spoolersh/pagedog/internal/prometheus"
	pagedog_spooler "github.com/spoolersh/pagedog/internal/spooler"
	"github.com/spoolersh/pagedog/internal/stamp"
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

// worker takes one incident off the input queue, investigates it, and puts
// the report on the output queue. The server runs one per incident in
// flight: a goroutine of its own process, or a process, [workerCommand],
// reading the same file. The secrets are read once, by [newWorker]; the
// clients are made per run.
type worker struct {
	spooler       spoolerConfig
	spoolerAPIKey string

	model modelConfig
	// modelAPIKey is the model's key; zero leaves the SDK to its own
	// variable.
	modelAPIKey string

	maxTurns int
	task     *template.Template

	tools toolsConfig
	// mcpHeaders are the headers of each MCP server of tools, read.
	mcpHeaders []map[string]string

	report      reportConfig
	githubToken string
}

// newWorker is the worker the file describes, ready to run: its secrets
// read from where the file says, its task read and parsed. What fails here
// fails once, before any incident.
func newWorker(ctx context.Context, cfg *config) (*worker, error) {
	spooler, path := cfg.workerSpooler()
	spoolerAPIKey, err := unwrap(ctx, path+".api_key", spooler.APIKey)
	if err != nil {
		return nil, err
	}
	w := &worker{
		spooler:       *spooler,
		spoolerAPIKey: spoolerAPIKey,
		model:         cfg.Worker.Model,
		maxTurns:      cfg.Worker.MaxTurns,
		tools:         cfg.Worker.Tools,
		report:        cfg.Worker.Report,
	}

	path, key, vars := modelKey(cfg.Worker.Model)
	if key.IsZero() {
		// The SDK's default is its own variable: missing, the first request
		// would fail instead of the start.
		if !anySet(vars) {
			return nil, fmt.Errorf(
				"%s: not set, and the environment has no %s",
				path, strings.Join(vars, " or "),
			)
		}
	} else {
		w.modelAPIKey, err = unwrap(ctx, path, key)
		if err != nil {
			return nil, err
		}
		// Empty, it would pass for the SDK's default, which it is not.
		if w.modelAPIKey == "" {
			return nil, fmt.Errorf(
				"%s: empty",
				path,
			)
		}
	}

	text, err := unwrap(ctx, "worker.task", cfg.Worker.Task)
	if err != nil {
		return nil, err
	}
	w.task, err = template.New("task").Parse(cmp.Or(text, defaultTask))
	if err != nil {
		return nil, fmt.Errorf(
			"worker.task: %w",
			err,
		)
	}
	// A field the template names wrongly fails here, once, not per incident.
	if err := w.task.Execute(io.Discard, taskData{}); err != nil {
		return nil, fmt.Errorf(
			"worker.task: %w",
			err,
		)
	}

	w.mcpHeaders, err = mcpHeaders(ctx, cfg.Worker.Tools.MCP)
	if err != nil {
		return nil, err
	}

	if g := cfg.Worker.Report.GitHub; g != nil {
		w.githubToken, err = unwrap(ctx, "worker.report.github.token", g.Token)
		if err != nil {
			return nil, err
		}
	}
	return w, nil
}

// modelKey is where a model's key comes from, with its path in the file:
// the key the file names, or, zero, the SDK's own variables of the
// environment, in the SDK's order.
func modelKey(m modelConfig) (path string, key pagedog_config.Secret, vars []string) {
	switch {
	case m.Claude != nil:
		return "worker.model.claude.api_key", m.Claude.APIKey, []string{"ANTHROPIC_API_KEY"}
	case m.OpenAI != nil:
		return "worker.model.openai.api_key", m.OpenAI.APIKey, []string{"OPENAI_API_KEY"}
	case m.Gemini != nil:
		return "worker.model.gemini.api_key", m.Gemini.APIKey, []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	}
	// parseConfig() checks that a model is set.
	return "worker.model", pagedog_config.Secret{}, nil
}

// anySet reports whether any of the variables is set, to something.
func anySet(names []string) bool {
	for _, name := range names {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// mcpHeaders reads the headers of each MCP server, the bearer token as the
// Authorization header among them, from where the file says.
func mcpHeaders(ctx context.Context, servers []mcpToolConfig) ([]map[string]string, error) {
	all := make([]map[string]string, 0, len(servers))
	for i, s := range servers {
		headers := make(map[string]string, len(s.Headers))
		for name, v := range s.Headers {
			var err error
			headers[name], err = unwrap(ctx,
				fmt.Sprintf("worker.tools.mcp[%d].headers.%s", i, name),
				v,
			)
			if err != nil {
				return nil, err
			}
		}
		if s.Auth != nil {
			token, err := unwrap(ctx,
				fmt.Sprintf("worker.tools.mcp[%d].auth.bearer", i),
				s.Auth.Bearer,
			)
			if err != nil {
				return nil, err
			}
			headers["Authorization"] = "Bearer " + token
		}
		all = append(all, headers)
	}
	return all, nil
}

// mcpClient is the client of an MCP server the file describes, with its
// headers read, named to the server as name. A command server gets the
// variables named for it, and nothing else of this process's.
func mcpClient(name string, s *mcpToolConfig, headers map[string]string) *pagedog_mcp.Client {
	c := &pagedog_mcp.Client{
		Name:    name,
		URL:     s.URL,
		Command: s.Command,
		Args:    s.Args,
		Stderr:  os.Stderr,
	}
	if s.URL != "" {
		c.Header = make(http.Header, len(headers))
		for name, v := range headers {
			c.Header.Set(name, v)
		}
	}
	for _, name := range s.Env {
		if v, has := os.LookupEnv(name); has {
			c.Env = append(c.Env, name+"="+v)
		}
	}
	return c
}

// workerCommand is pagedog worker: a [worker] as a process of its own,
// reading the file the server does, the log on its stderr.
type workerCommand struct {
	configPath string
	logLevel   slog.Level
}

func (c *workerCommand) Summary() string {
	return "Takes incidents off the queue, investigates each, and reports."
}

func (c *workerCommand) Epilog() string {
	return "The file is the server's. Of it, the worker section is read, and " +
		"spooler, unless worker.spooler names the Spooler the worker reaches."
}

func (c *workerCommand) DefineFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath,
		"config", "pagedog.yaml",
		"configuration `file`",
	)
	fs.Var(LogLevelVar(&c.logLevel),
		"log-level",
		"log level",
	)
}

func (c *workerCommand) Run(ctx context.Context, _ []string) error {
	data, err := os.ReadFile(c.configPath)
	if err != nil {
		return err
	}
	cfg, err := parseWorkerConfig(data)
	if err != nil {
		return fmt.Errorf(
			"%s: %w",
			c.configPath, err,
		)
	}

	// The settings as parsed, secrets as where they come from, before the
	// logs begin; a goroutine worker prints none, its server did.
	if err := printBanner(os.Stderr, workerBannerTemplate, makeWorkerBanner(&cfg, c.configPath, c.logLevel)); err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: c.logLevel,
	})).With("version", stamp.Version())

	w, err := newWorker(ctx, &cfg)
	if err != nil {
		return err
	}
	return w.run(ctx, logger)
}

// run takes one incident off the queue, investigates it and reports, under
// the given log.
func (w *worker) run(ctx context.Context, logger *slog.Logger) error {
	spoolerClient := spooler.Client{
		Trace: spooler.LoggerClientTrace(logger.With(
			"component", "spooler",
		)),
		Host:               w.spooler.Host,
		APIKey:             w.spoolerAPIKey,
		InsecureDisableTLS: w.spooler.NoTLS,
	}

	var reportStore pagedog.ReportStore
	switch r := w.report; {
	case r.FS != nil:
		reportStore = &pagedog_fs.ReportStore{
			Trace: pagedog_fs.LoggerReportStoreTrace(logger.With(
				"component", "report",
			)),
			Dir: r.FS.Dir,
		}

	case r.GitHub != nil:
		client, err := google_github.NewClient(google_github.WithAuthToken(w.githubToken))
		if err != nil {
			return err
		}
		reportStore = &pagedog_github.ReportStore{
			Trace: pagedog_github.LoggerReportStoreTrace(logger.With(
				"component", "report",
			)),
			Client: client,
			Owner:  r.GitHub.Owner,
			Repo:   r.GitHub.Repo,
		}

	default:
		// parseConfig() checks that a store is set.
		return errors.New("no report store")
	}

	// A name or a limit left zero is the package's default. A key left zero
	// leaves the SDK to its own variable.
	var model ai.Model
	switch m := w.model; {
	case m.Claude != nil:
		var opts []anthropic_option.RequestOption
		if w.modelAPIKey != "" {
			opts = append(opts, anthropic_option.WithAPIKey(w.modelAPIKey))
		}
		model = &claude.Model{
			Client:    anthropic.NewClient(opts...),
			Name:      anthropic.Model(m.Claude.Name),
			MaxTokens: m.Claude.MaxTokens,
		}

	case m.OpenAI != nil:
		var opts []openai_option.RequestOption
		if w.modelAPIKey != "" {
			opts = append(opts, openai_option.WithAPIKey(w.modelAPIKey))
		}
		model = &pagedog_openai.Model{
			Client:    openai.NewClient(opts...),
			Name:      m.OpenAI.Name,
			MaxTokens: m.OpenAI.MaxTokens,
		}

	case m.Gemini != nil:
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey: w.modelAPIKey,
		})
		if err != nil {
			return err
		}
		model = &gemini.Model{
			Client:    client,
			Name:      m.Gemini.Name,
			MaxTokens: m.Gemini.MaxTokens,
		}

	default:
		// parseConfig() checks that a model is set.
		return errors.New("no model")
	}

	agent := ai.Agent{
		Trace: ai.LoggerAgentTrace(logger.With(
			"component", "agent",
		)),
		Model: model,
	}
	var tools []ai.Tool
	if p := w.tools.Prometheus; p != nil {
		src := &prometheus.Client{
			URL: p.URL,
		}
		tools = append(tools,
			&prometheus.RangeQueryTool{
				Source: src,
			},
			&prometheus.MetricNamesTool{
				Source: src,
			},
		)
	}
	if l := w.tools.Loki; l != nil {
		src := &loki.Client{
			URL: l.URL,
		}
		tools = append(tools,
			&loki.RangeQueryTool{
				Source: src,
			},
			&loki.LabelsTool{
				Source: src,
			},
		)
	}
	for i, s := range w.tools.MCP {
		client := mcpClient("pagedog-worker", &s, w.mcpHeaders[i])
		ts, err := client.Tools(ctx, s.Tools)
		if err != nil {
			return fmt.Errorf(
				"mcp server %q: %w",
				s.Name, err,
			)
		}
		// The connection lasts as long as this process; a child process
		// server is stopped with it.
		defer client.Close()
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
	for _, t := range tools {
		if err := agent.RegisterTool(t); err != nil {
			return err
		}
	}

	pipe := &pagedog_spooler.Pipe[pagedog.Incident, pagedog.IncidentReport]{
		Client: &spoolerClient,
		Spool:  w.spooler.Spool,
		Input:  w.spooler.Incidents,
		Output: w.spooler.Reports,

		// One report per incident: a send repeated on an unknown result
		// is one message on the reports queue.
		Dedup: func(r pagedog.IncidentReport) string {
			return r.Incident.Key
		},
	}
	err := pipe.Pump(ctx, func(sub context.Context, inc pagedog.Incident) (r pagedog.IncidentReport, err error) {
		var (
			now       = time.Now().UTC()
			startedAt = cmp.Or(inc.StartedAt, now)
			sb        strings.Builder
		)
		err = w.task.Execute(&sb, taskData{
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
			MaxTurns: w.maxTurns,
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
