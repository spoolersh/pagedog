// Pagedog receives incident notifications over HTTP, puts the incidents on a
// Spooler queue, scales worker processes to the queue, and passes the
// workers' reports on.
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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gobwas/flagutil"
	"github.com/gobwas/flagutil/parse/pargs"
	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/pagedog/cmd"
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
		configPath = "pagedog.yaml"
		logLevel   = slog.LevelInfo

		// The NO_COLOR convention: set and non-empty means no color,
		// whatever the value.
		noColor = os.Getenv("NO_COLOR") != ""
	)
	fs := flag.NewFlagSet("pagedog", flag.ExitOnError)
	fs.StringVar(&configPath,
		"config", configPath,
		"configuration `file`",
	)
	fs.Var(cmd.LogLevelVar(&logLevel),
		"log-level",
		"log level",
	)
	fs.BoolVar(&noColor,
		"no-color", noColor,
		"do not color the output; a non-empty $NO_COLOR sets it too",
	)
	_ = flagutil.Parse(ctx, fs,
		flagutil.WithCustomUsage(),
		flagutil.WithParser(&pargs.Parser{
			Args: os.Args[1:],
		}),
	)

	data, err := os.ReadFile(configPath)
	if err != nil {
		fail(err)
	}
	cfg, err := parseConfig(data)
	if err != nil {
		fail(fmt.Errorf(
			"%s: %w",
			configPath, err,
		))
	}
	if f := cfg.Worker.TaskFile; f != "" {
		// The worker has no file: the task travels on its command line.
		task, err := os.ReadFile(f)
		if err != nil {
			fail(fmt.Errorf(
				"worker.task_file: %w",
				err,
			))
		}
		cfg.Worker.Task = string(task)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	apiKey := unwrap(ctx, "spooler.api_key", cfg.Spooler.APIKey)
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
		Queue:  cfg.Spooler.Incidents.Name,
		Dedup: func(inc pagedog.Incident) string {
			return inc.Key
		},
	}
	reports := pagedog_spooler.Queue[pagedog.IncidentReport]{
		Client: &spoolerClient,
		Spool:  cfg.Spooler.Spool,
		Queue:  cfg.Spooler.Reports.Name,
	}

	for _, q := range []queueConfig{
		cfg.Spooler.Incidents,
		cfg.Spooler.Reports,
	} {
		settings := queueSettings(q)
		queue, err := spoolerClient.CreateQueue(ctx, spooler.CreateQueueRequest{
			Spool:    cfg.Spooler.Spool,
			Queue:    q.Name,
			Settings: settings,
		})
		if errors.Is(err, spooler.ErrQueueExists) {
			if settings == (spooler.QueueSettingsCreate{}) {
				// Nothing to apply, and an empty update is refused.
				continue
			}
			// Apply the settings to the existing queue, so a changed
			// setting takes effect across restarts.
			err = spoolerClient.UpdateQueue(ctx, spooler.UpdateQueueRequest{
				Spool: cfg.Spooler.Spool,
				Queue: q.Name,
				Settings: spooler.QueueSettingsUpdate{
					LeaseTimeoutSeconds: settings.LeaseTimeoutSeconds,
					MaxRetries:          settings.MaxRetries,
					DedupWindowSeconds:  settings.DedupWindowSeconds,
				},
			})
			if err != nil {
				fail(err)
			}
			logger.Info("updated queue",
				"name", q.Name,
			)
			continue
		}
		if err != nil {
			fail(err)
		}
		logger.Info("created queue",
			"name", queue.Name,
			"uid", queue.UID,
		)
	}

	webhookServer := webhook.Server{
		Trace: webhook.LoggerServerTrace(logger.With(
			"component", "webhook",
		)),
		Queue: &incidents,
	}
	for i, s := range cfg.Webhook.Sources {
		var err error
		switch {
		case s.Alertmanager != nil:
			err = webhookServer.RegisterSource("alertmanager", &alertmanager.Source{
				Token: unwrap(ctx,
					fmt.Sprintf("webhook.sources[%d].alertmanager.token", i),
					s.Alertmanager.Token,
				),
			})
		case s.IncidentIO != nil:
			err = webhookServer.RegisterSource("incidentio", &incidentio.Source{
				Trace: incidentio.LoggerSourceTrace(logger.With(
					"component", "webhook",
				)),
				SigningSecret: unwrap(ctx,
					fmt.Sprintf("webhook.sources[%d].incidentio.signing_secret", i),
					s.IncidentIO.SigningSecret,
				),
			})
		case s.PagerDuty != nil:
			err = webhookServer.RegisterSource("pagerduty", &pagerduty.Source{
				Trace: pagerduty.LoggerSourceTrace(logger.With(
					"component", "webhook",
				)),
				Secret: unwrap(ctx,
					fmt.Sprintf("webhook.sources[%d].pagerduty.secret", i),
					s.PagerDuty.Secret,
				),
			})
		}
		if err != nil {
			fail(err)
		}
	}

	// The worker's flags mirror the launcher's configuration. Its secrets
	// reach it in its environment, read here from where the file says.
	args := workerArgs(&cfg, logLevel)

	var (
		workerRuntime runtime.Runtime

		// closeRuntime takes the workers down with the launcher, for a
		// runtime whose workers cannot outlive it.
		closeRuntime func(context.Context) error
		stopTimeout  time.Duration
	)
	switch {
	case cfg.Workers.Runtime.Exec != nil:
		c := cfg.Workers.Runtime.Exec
		r := &exec.Runtime{
			Trace:       exec.LoggerTrace(logger.With("component", "exec")),
			Path:        c.Path,
			Args:        args,
			Env:         workerEnv(ctx, "workers.runtime.exec.env", c.Env),
			NoColor:     noColor,
			StopTimeout: c.StopTimeout,
		}
		workerRuntime = r
		closeRuntime = r.Close
		stopTimeout = cmp.Or(c.StopTimeout, exec.DefaultStopTimeout)

	case cfg.Workers.Runtime.Docker != nil:
		c := cfg.Workers.Runtime.Docker
		workerRuntime = &docker.Runtime{
			Trace:       docker.LoggerTrace(logger.With("component", "docker")),
			Image:       c.Image,
			Args:        args,
			Env:         workerEnv(ctx, "workers.runtime.docker.env", c.Env),
			Network:     c.Network,
			Binds:       c.Binds,
			StopTimeout: c.StopTimeout,
		}

	default:
		// parseConfig() checks that a runtime is set.
		fail(errors.New("no worker runtime"))
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
	store := reportStore(cfg.Worker.Report, logger)

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
			notifiers = append(notifiers, &webhook.Notifier{
				Trace: webhook.LoggerNotifierTrace(logger.With(
					"component", "notify",
				)),
				URL: n.Webhook.URL,
				Token: unwrap(ctx,
					fmt.Sprintf("notify[%d].webhook.token", i),
					n.Webhook.Token,
				),
				Store: store,
			})
		case n.Slack != nil:
			notifiers = append(notifiers, &slack.Notifier{
				Trace: slack.LoggerTrace(logger.With(
					"component", "notify",
				)),
				URL: unwrap(ctx,
					fmt.Sprintf("notify[%d].slack.url", i),
					n.Slack.URL,
				),
				Store: store,
			})
		case n.IncidentIO != nil:
			notifiers = append(notifiers, &incidentio.Notifier{
				Trace: incidentio.LoggerNotifierTrace(logger.With(
					"component", "notify",
				)),
				APIKey: unwrap(ctx,
					fmt.Sprintf("notify[%d].incidentio.api_key", i),
					n.IncidentIO.APIKey,
				),
				APIURL: n.IncidentIO.URL,
				Store:  store,
			})
		case n.PagerDuty != nil:
			notifiers = append(notifiers, &pagerduty.Notifier{
				Trace: pagerduty.LoggerNotifierTrace(logger.With(
					"component", "notify",
				)),
				APIKey: unwrap(ctx,
					fmt.Sprintf("notify[%d].pagerduty.api_key", i),
					n.PagerDuty.APIKey,
				),
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
		fail(err)
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
	if errors.Is(err, context.Canceled) {
		// A signal: the loops return the cause of the group's context,
		// which is the cancellation itself.
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
	if err != nil {
		fail(err)
	}
}

// reportStore builds the report store the workers are configured with, for
// the notifiers to read the reports back through.
func reportStore(cfg reportConfig, logger *slog.Logger) pagedog.ReportStore {
	switch {
	case cfg.FS != nil:
		return &pagedog_fs.ReportStore{
			Trace: pagedog_fs.LoggerTrace(logger.With(
				"component", "report",
			)),
			Dir: cfg.FS.Dir,
		}
	default:
		// parseConfig() checks that a store is set.
		fail(errors.New("no report store"))
		return nil
	}
}

// workerArgs is the worker's flags for a configuration: what a worker needs
// of it, and of what it has a default for only what the file sets.
func workerArgs(cfg *config, logLevel slog.Level) []string {
	args := []string{
		"--log-level", cmd.LogLevelVar(&logLevel).String(),
		"--spooler.host", cfg.Spooler.Host,
		"--spooler.no-tls=" + strconv.FormatBool(cfg.Spooler.NoTLS),
		"--spooler.spool", cfg.Spooler.Spool,
		"--spooler.input", cfg.Spooler.Incidents.Name,
		"--spooler.output", cfg.Spooler.Reports.Name,
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
			var headers map[string]cmd.MCPHeader
			if len(m.Headers) > 0 {
				headers = make(map[string]cmd.MCPHeader, len(m.Headers))
				for name, v := range m.Headers {
					if v.IsSecret() {
						headers[name] = cmd.MCPHeader{Env: v.Secret.Env}
					} else {
						headers[name] = cmd.MCPHeader{Value: v.Literal}
					}
				}
			}
			// Strings, and lists and maps of them: marshaling cannot fail.
			server, _ := json.Marshal(cmd.MCPServer{
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
func workerEnv(ctx context.Context, path string, env map[string]pagedog_config.Value) map[string]string {
	kv := make(map[string]string, len(env))
	for name, v := range env {
		kv[name] = unwrap(ctx, path+"."+name, v)
	}
	return kv
}

// unwrap reads a secret, or a value that may be one, or fails naming its
// place in the configuration.
func unwrap(ctx context.Context, path string, s interface {
	Unwrap(context.Context) (string, error)
}) string {
	v, err := s.Unwrap(ctx)
	if err != nil {
		fail(fmt.Errorf(
			"%s: %w",
			path, err,
		))
	}
	return v
}

// queueSettings turns a queue's configuration into the settings to create
// it with; a setting left out stays unset.
func queueSettings(q queueConfig) (s spooler.QueueSettingsCreate) {
	if d := q.LeaseTimeout; d != 0 {
		s.LeaseTimeoutSeconds = new(int(d / time.Second))
	}
	s.MaxRetries = q.MaxRetries
	if d := q.DedupWindow; d != 0 {
		s.DedupWindowSeconds = new(int(d / time.Second))
	}
	return s
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "fatal error: %v\n", err)
	os.Exit(1)
}
