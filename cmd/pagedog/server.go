package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/pagedog/internal/alertmanager"
	pagedog_config "github.com/spoolersh/pagedog/internal/config"
	pagedog_fs "github.com/spoolersh/pagedog/internal/fs"
	"github.com/spoolersh/pagedog/internal/httputil"
	"github.com/spoolersh/pagedog/internal/incidentio"
	"github.com/spoolersh/pagedog/internal/pagerduty"
	"github.com/spoolersh/pagedog/internal/runtime"
	"github.com/spoolersh/pagedog/internal/runtime/docker"
	"github.com/spoolersh/pagedog/internal/runtime/exec"
	"github.com/spoolersh/pagedog/internal/slack"
	pagedog_spooler "github.com/spoolersh/pagedog/internal/spooler"
	"github.com/spoolersh/pagedog/internal/webhook"
	"github.com/spoolersh/spooler-go"
	"golang.org/x/sync/errgroup"
)

// serverCommand serves the webhook, keeps the workers running and delivers
// the reports: the process that stays up.
type serverCommand struct {
	configPath string
	logLevel   slog.Level
	noColor    bool
}

func (c *serverCommand) Summary() string {
	return "Serves the webhook, runs the workers and delivers the reports."
}

func (c *serverCommand) DefineFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath,
		"config", "pagedog.yaml",
		"configuration `file`",
	)
	fs.Var(LogLevelVar(&c.logLevel),
		"log-level",
		"log level",
	)
	// The NO_COLOR convention: set and non-empty means no color, whatever
	// the value.
	fs.BoolVar(&c.noColor,
		"no-color", os.Getenv("NO_COLOR") != "",
		"do not color the output; a non-empty $NO_COLOR sets it too",
	)
}

func (c *serverCommand) Run(ctx context.Context, _ []string) error {
	data, err := os.ReadFile(c.configPath)
	if err != nil {
		return err
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return fmt.Errorf(
			"%s: %w",
			c.configPath, err,
		)
	}
	if f := cfg.Worker.TaskFile; f != "" {
		// The worker has no file: the task travels on its command line.
		task, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf(
				"worker.task_file: %w",
				err,
			)
		}
		cfg.Worker.Task = string(task)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: c.logLevel,
	}))

	apiKey, err := unwrap(ctx, "spooler.api_key", cfg.Spooler.APIKey)
	if err != nil {
		return err
	}
	spoolerClient := spooler.Client{
		Trace: spooler.LoggerClientTrace(logger.With(
			"component", "spooler",
		)),
		Host:               cfg.Spooler.Host,
		APIKey:             apiKey,
		InsecureDisableTLS: cfg.Spooler.NoTLS,
	}
	incidents := pagedog_spooler.Queue[pagedog.Incident]{
		Client: &spoolerClient,
		Spool:  cfg.Spooler.Spool,
		Queue:  cfg.Spooler.Incidents,
		Dedup: func(inc pagedog.Incident) string {
			return inc.Key
		},
	}
	reports := pagedog_spooler.Queue[pagedog.IncidentReport]{
		Client: &spoolerClient,
		Spool:  cfg.Spooler.Spool,
		Queue:  cfg.Spooler.Reports,
	}

	// The queues are made elsewhere; one missing is a failure to start,
	// not one to log every tick.
	for _, name := range []string{
		cfg.Spooler.Incidents,
		cfg.Spooler.Reports,
	} {
		_, err := spoolerClient.Queue(ctx, spooler.QueueRequest{
			Spool: cfg.Spooler.Spool,
			Queue: name,
		})
		if errors.Is(err, spooler.ErrQueueNotFound) {
			return fmt.Errorf(
				"queue %q does not exist on spool %q: create it first",
				name, cfg.Spooler.Spool,
			)
		}
		if err != nil {
			return err
		}
	}

	webhookServer := webhook.Server{
		Trace: webhook.LoggerServerTrace(logger.With(
			"component", "webhook",
		)),
		Queue: &incidents,
	}
	for i, s := range cfg.Webhook.Sources {
		switch {
		case s.Alertmanager != nil:
			token, err := unwrap(ctx,
				fmt.Sprintf("webhook.sources[%d].alertmanager.token", i),
				s.Alertmanager.Token,
			)
			if err != nil {
				return err
			}
			err = webhookServer.RegisterSource("alertmanager", &alertmanager.Source{
				Token: token,
			})
			if err != nil {
				return err
			}
		case s.IncidentIO != nil:
			secret, err := unwrap(ctx,
				fmt.Sprintf("webhook.sources[%d].incidentio.signing_secret", i),
				s.IncidentIO.SigningSecret,
			)
			if err != nil {
				return err
			}
			err = webhookServer.RegisterSource("incidentio", &incidentio.Source{
				Trace: incidentio.LoggerSourceTrace(logger.With(
					"component", "webhook",
				)),
				SigningSecret: secret,
			})
			if err != nil {
				return err
			}
		case s.PagerDuty != nil:
			secret, err := unwrap(ctx,
				fmt.Sprintf("webhook.sources[%d].pagerduty.secret", i),
				s.PagerDuty.Secret,
			)
			if err != nil {
				return err
			}
			err = webhookServer.RegisterSource("pagerduty", &pagerduty.Source{
				Trace: pagerduty.LoggerSourceTrace(logger.With(
					"component", "webhook",
				)),
				Secret: secret,
			})
			if err != nil {
				return err
			}
		}
	}

	// The worker's flags mirror the launcher's configuration. Its secrets
	// reach it in its environment, read here from where the file says.
	args := workerArgs(&cfg, c.logLevel)

	var (
		workerRuntime runtime.Runtime

		// closeRuntime takes the workers down with the launcher, for a
		// runtime whose workers cannot outlive it.
		closeRuntime func(context.Context) error
		stopTimeout  time.Duration
	)
	switch {
	case cfg.Workers.Runtime.Exec != nil:
		e := cfg.Workers.Runtime.Exec
		path := e.Path
		if path == "" {
			// The worker is this program.
			path, err = os.Executable()
			if err != nil {
				return err
			}
		}
		env, err := workerEnv(ctx, "workers.runtime.exec.env", e.Env)
		if err != nil {
			return err
		}
		r := &exec.Runtime{
			Trace:       exec.LoggerTrace(logger.With("component", "exec")),
			Path:        path,
			Args:        args,
			Env:         env,
			NoColor:     c.noColor,
			StopTimeout: e.StopTimeout,
		}
		workerRuntime = r
		closeRuntime = r.Close
		stopTimeout = cmp.Or(e.StopTimeout, exec.DefaultStopTimeout)

	case cfg.Workers.Runtime.Docker != nil:
		d := cfg.Workers.Runtime.Docker
		env, err := workerEnv(ctx, "workers.runtime.docker.env", d.Env)
		if err != nil {
			return err
		}
		workerRuntime = &docker.Runtime{
			Trace:       docker.LoggerTrace(logger.With("component", "docker")),
			Image:       d.Image,
			Args:        args,
			Env:         env,
			Network:     d.Network,
			Binds:       d.Binds,
			StopTimeout: d.StopTimeout,
		}

	default:
		// parseConfig() checks that a runtime is set.
		return errors.New("no worker runtime")
	}

	reconciler := runtime.Reconciler{
		Trace: runtime.LoggerReconcilerTrace(logger.With(
			"component", "runtime",
		)),
		Queue:      &incidents,
		Runtime:    workerRuntime,
		MinWorkers: cfg.Workers.Min,
		MaxWorkers: cfg.Workers.Max,
	}

	// The notifiers read the reports back from where the workers put them.
	store, err := reportStore(cfg.Worker.Report, logger)
	if err != nil {
		return err
	}

	var notifiers pagedog.Notifiers
	for i, n := range cfg.Notify {
		switch {
		case n.Log != nil:
			notifiers = append(notifiers, pagedog.LogNotifier{
				Logger: logger.With(
					"component", "report",
				),
			})
		case n.Webhook != nil:
			token, err := unwrap(ctx,
				fmt.Sprintf("notify[%d].webhook.token", i),
				n.Webhook.Token,
			)
			if err != nil {
				return err
			}
			notifiers = append(notifiers, &webhook.Notifier{
				Trace: webhook.LoggerNotifierTrace(logger.With(
					"component", "notify",
				)),
				URL:   n.Webhook.URL,
				Token: token,
				Store: store,
			})
		case n.Slack != nil:
			url, err := unwrap(ctx,
				fmt.Sprintf("notify[%d].slack.url", i),
				n.Slack.URL,
			)
			if err != nil {
				return err
			}
			notifiers = append(notifiers, &slack.Notifier{
				Trace: slack.LoggerTrace(logger.With(
					"component", "notify",
				)),
				URL:   url,
				Store: store,
			})
		case n.IncidentIO != nil:
			key, err := unwrap(ctx,
				fmt.Sprintf("notify[%d].incidentio.api_key", i),
				n.IncidentIO.APIKey,
			)
			if err != nil {
				return err
			}
			notifiers = append(notifiers, &incidentio.Notifier{
				Trace: incidentio.LoggerNotifierTrace(logger.With(
					"component", "notify",
				)),
				APIKey: key,
				APIURL: n.IncidentIO.URL,
				Store:  store,
			})
		case n.PagerDuty != nil:
			key, err := unwrap(ctx,
				fmt.Sprintf("notify[%d].pagerduty.api_key", i),
				n.PagerDuty.APIKey,
			)
			if err != nil {
				return err
			}
			notifiers = append(notifiers, &pagerduty.Notifier{
				Trace: pagerduty.LoggerNotifierTrace(logger.With(
					"component", "notify",
				)),
				APIKey: key,
				From:   n.PagerDuty.From,
				APIURL: n.PagerDuty.URL,
				Store:  store,
			})
		}
	}

	mux := http.NewServeMux()
	// The webhook server routes on the source name alone, so the mount
	// point is stripped before it sees the path.
	mux.Handle("/webhook/", http.StripPrefix("/webhook", webhookServer.Handler()))

	// Listen before anything runs: a busy port fails here, and the address
	// logged is the one bound, which matters when the port is 0.
	ln, err := net.Listen("tcp", cfg.Webhook.Listen)
	if err != nil {
		return err
	}
	logger.Info("listening incoming webhooks",
		"addr", ln.Addr().String(),
	)
	server := &http.Server{
		Handler: &httputil.TraceHandler{
			Trace: httputil.LoggerTrace(logger.With(
				"component", "http",
			)),
			Handler: mux,
		},
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		// The group's context ends on a signal and when any other goroutine
		// fails.
		<-gctx.Done()
		sub, cancel := context.WithTimeout(
			context.WithoutCancel(gctx),
			5*time.Second,
		)
		defer cancel()
		return server.Shutdown(sub)
	})
	g.Go(func() error {
		return reconciler.Loop(gctx)
	})
	if len(notifiers) > 0 {
		g.Go(func() error {
			// Reports go to the notifiers; one they refuse is nacked and
			// comes back. A drain that fails starts over after a pause,
			// as the reconciler goes on after a failed tick.
			for {
				err := reports.Drain(gctx, notifiers.Notify)
				if gctx.Err() != nil {
					return err
				}
				logger.Error("failed draining reports; retrying",
					"error", err,
				)
				select {
				case <-time.After(time.Second):
				case <-gctx.Done():
					return context.Cause(gctx)
				}
			}
		})
	}
	g.Go(func() error {
		err := server.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			// Shut down by the goroutine above: not a failure.
			return nil
		}
		return err
	})
	err = g.Wait()
	if ctx.Err() != nil {
		// A signal: the loops return its cause, and the runner reports it.
		err = nil
	}
	if closeRuntime != nil {
		// The workers are this process's children, and the reconciler
		// leaves them running: take them down before leaving. A kill after
		// the stop timeout is the longest one takes.
		sub, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			stopTimeout+time.Second,
		)
		err = errors.Join(err, closeRuntime(sub))
		cancel()
	}
	return err
}

// reportStore builds the report store the workers are configured with, for
// the notifiers to read the reports back through.
func reportStore(cfg reportConfig, logger *slog.Logger) (pagedog.ReportStore, error) {
	switch {
	case cfg.FS != nil:
		return &pagedog_fs.ReportStore{
			Trace: pagedog_fs.LoggerTrace(logger.With(
				"component", "report",
			)),
			Dir: cfg.FS.Dir,
		}, nil
	default:
		// parseConfig() checks that a store is set.
		return nil, errors.New("no report store")
	}
}

// workerArgs is the worker's command line for a configuration: the command,
// then what a worker needs of the file, and of what it has a default for
// only what the file sets.
func workerArgs(cfg *config, logLevel slog.Level) []string {
	args := []string{
		"worker",
		"--log-level", LogLevelVar(&logLevel).String(),
		"--spooler.host", cfg.Spooler.Host,
		"--spooler.no-tls=" + strconv.FormatBool(cfg.Spooler.NoTLS),
		"--spooler.spool", cfg.Spooler.Spool,
		"--spooler.input", cfg.Spooler.Incidents,
		"--spooler.output", cfg.Spooler.Reports,
	}
	// Every model takes a name and a token limit, under its own kind.
	var (
		modelKind      string
		modelName      string
		modelMaxTokens int
	)
	switch m := cfg.Worker.Model; {
	case m.Claude != nil:
		modelKind, modelName, modelMaxTokens = "claude", m.Claude.Name, m.Claude.MaxTokens
	case m.OpenAI != nil:
		modelKind, modelName, modelMaxTokens = "openai", m.OpenAI.Name, m.OpenAI.MaxTokens
	case m.Gemini != nil:
		modelKind, modelName, modelMaxTokens = "gemini", m.Gemini.Name, m.Gemini.MaxTokens
	}
	if modelKind != "" {
		args = append(args, "--agent.model", modelKind)
		if modelName != "" {
			args = append(args, "--agent."+modelKind+".name", modelName)
		}
		if modelMaxTokens != 0 {
			args = append(args, "--agent."+modelKind+".max-tokens", strconv.Itoa(modelMaxTokens))
		}
	}
	if n := cfg.Worker.MaxTurns; n != 0 {
		args = append(args, "--agent.max-turns", strconv.Itoa(n))
	}
	if t := cfg.Worker.Task; t != "" {
		args = append(args, "--agent.task", t)
	}
	var (
		tools    []string
		toolArgs []string
	)
	if p := cfg.Worker.Tools.Prometheus; p != nil {
		tools = append(tools, "prometheus")
		toolArgs = append(toolArgs, "--agent.prometheus.url", p.URL)
	}
	if l := cfg.Worker.Tools.Loki; l != nil {
		tools = append(tools, "loki")
		toolArgs = append(toolArgs, "--agent.loki.url", l.URL)
	}
	if len(cfg.Worker.Tools.MCP) > 0 {
		tools = append(tools, "mcp")
		for _, m := range cfg.Worker.Tools.MCP {
			// A secret header goes as the name of the worker's variable
			// that holds it: nothing secret is on a command line.
			var headers map[string]MCPHeader
			if len(m.Headers) > 0 {
				headers = make(map[string]MCPHeader, len(m.Headers))
				for name, v := range m.Headers {
					if v.IsSecret() {
						headers[name] = MCPHeader{Env: v.Secret.Env}
					} else {
						headers[name] = MCPHeader{Value: v.Literal}
					}
				}
			}
			// Strings, and lists and maps of them: marshaling cannot fail.
			server, _ := json.Marshal(MCPServer{
				Name:    m.Name,
				URL:     m.URL,
				Headers: headers,
				Command: m.Command,
				Args:    m.Args,
				Env:     m.Env,
				Tools:   m.Tools,
			})
			toolArgs = append(toolArgs, "--agent.mcp.server", string(server))
		}
	}
	if len(tools) > 0 {
		args = append(args, "--agent.tools", strings.Join(tools, ","))
		args = append(args, toolArgs...)
	}
	if r := cfg.Worker.Report.FS; r != nil {
		args = append(args,
			"--report.store", "fs",
			"--report.fs.dir", r.Dir,
		)
	}
	return args
}

// workerEnv reads a worker's environment configured at path: the literals
// as written, the secrets from where the file says.
func workerEnv(ctx context.Context, path string, env map[string]pagedog_config.Value) (map[string]string, error) {
	kv := make(map[string]string, len(env))
	for name, v := range env {
		s, err := unwrap(ctx, path+"."+name, v)
		if err != nil {
			return nil, err
		}
		kv[name] = s
	}
	return kv, nil
}

// unwrap reads a secret, or a value that may be one, naming its place in
// the configuration in the error.
func unwrap(ctx context.Context, path string, s interface {
	Unwrap(context.Context) (string, error)
}) (string, error) {
	v, err := s.Unwrap(ctx)
	if err != nil {
		return "", fmt.Errorf(
			"%s: %w",
			path, err,
		)
	}
	return v, nil
}
