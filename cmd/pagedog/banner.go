package main

import (
	"cmp"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/spoolersh/pagedog/internal/ai"
	"github.com/spoolersh/pagedog/internal/claude"
	pagedog_config "github.com/spoolersh/pagedog/internal/config"
	"github.com/spoolersh/pagedog/internal/gemini"
	pagedog_openai "github.com/spoolersh/pagedog/internal/openai"
	"github.com/spoolersh/pagedog/internal/runtime"
	"github.com/spoolersh/pagedog/internal/stamp"
)

//go:embed server_banner.txt
var serverBannerText string

//go:embed worker_banner.txt
var workerBannerText string

var (
	serverBannerTemplate = template.Must(template.New("server").Parse(serverBannerText))
	workerBannerTemplate = template.Must(template.New("worker").Parse(workerBannerText))
)

// printBanner renders a startup banner to w, its columns aligned on tabs.
func printBanner(w io.Writer, t *template.Template, info any) error {
	tw := tabwriter.NewWriter(w, 0, 8, 1, ' ', 0)
	if err := t.Execute(tw, info); err != nil {
		return err
	}
	return tw.Flush()
}

// line is one key and its value in a banner's list.
type line struct {
	Key   string
	Value string
}

// serverBanner is the parsed configuration as the server's banner renders
// it: every secret as where it comes from, never as its value.
type serverBanner struct {
	Version  string
	Revision string
	Built    string
	Config   string
	LogLevel string

	Spooler string
	APIKey  string
	Queues  string

	Listen  string
	Sources []line

	Workers string
	Env     []line

	// Files are what a docker worker gets before it starts, each as its
	// path in the container and where it is read from.
	Files []line

	// WorkerSpooler is the Spooler the workers reach when it is not this
	// process's; empty otherwise.
	WorkerSpooler string

	Model    string
	ModelKey string
	Turns    string
	Task     string
	Tools    []string
	Report   string

	Notify []string
}

func makeServerBanner(cfg *config, configPath string, logLevel slog.Level) serverBanner {
	b := serverBanner{
		Version:  stamp.Version(),
		Revision: stamp.Revision(),
		Built:    stamp.Built(),
		Config:   configPath,
		LogLevel: LogLevelVar(&logLevel).String(),
		Spooler:  spoolerLine(&cfg.Spooler),
		APIKey:   source(cfg.Spooler.APIKey),
		Queues:   queuesLine(&cfg.Spooler),
		Listen:   cfg.Webhook.Listen,
	}
	for _, s := range cfg.Webhook.Sources {
		switch {
		case s.Alertmanager != nil:
			b.Sources = append(b.Sources, line{"alertmanager", "token " + source(s.Alertmanager.Token)})
		case s.IncidentIO != nil:
			b.Sources = append(b.Sources, line{"incidentio", "signing secret " + source(s.IncidentIO.SigningSecret)})
		case s.PagerDuty != nil:
			b.Sources = append(b.Sources, line{"pagerduty", "secret " + source(s.PagerDuty.Secret)})
		}
	}
	var env map[string]pagedog_config.Value
	switch r := cfg.Workers.Runtime; {
	case r.Exec != nil:
		b.Workers = "exec " + r.Exec.Path
		if len(r.Exec.Args) > 0 {
			b.Workers += " " + strings.Join(r.Exec.Args, " ")
		}
		env = r.Exec.Env
	case r.Docker != nil:
		b.Workers = "docker " + r.Docker.Image
		if len(r.Docker.Command) > 0 {
			b.Workers += " " + strings.Join(r.Docker.Command, " ")
		}
		if r.Docker.Network != "" {
			b.Workers += ", network " + r.Docker.Network
		}
		env = r.Docker.Env
		for _, dst := range slices.Sorted(maps.Keys(r.Docker.Files)) {
			b.Files = append(b.Files, line{dst, "from " + r.Docker.Files[dst]})
		}
	default:
		b.Workers = "goroutines of this process"
	}
	b.Workers += ", max " + strconv.Itoa(cmp.Or(cfg.Workers.Max, runtime.DefaultMaxWorkers))
	if cfg.Workers.Min > 0 {
		b.Workers += ", min " + strconv.Itoa(cfg.Workers.Min)
	}
	for _, name := range slices.Sorted(maps.Keys(env)) {
		b.Env = append(b.Env, line{name, value(env[name])})
	}
	if s := cfg.Worker.Spooler; s != nil {
		b.WorkerSpooler = spoolerLine(s) + ", api key " + source(s.APIKey) + ", queues " + queuesLine(s)
	}
	b.Model, b.ModelKey, b.Turns, b.Task, b.Tools, b.Report = workerLines(&cfg.Worker)
	for _, n := range cfg.Notify {
		switch {
		case n.Log != nil:
			b.Notify = append(b.Notify, "log")
		case n.Webhook != nil:
			s := "webhook " + n.Webhook.URL
			if !n.Webhook.Token.IsZero() {
				s += ", token " + source(n.Webhook.Token)
			}
			b.Notify = append(b.Notify, s)
		case n.Slack != nil:
			b.Notify = append(b.Notify, "slack, url "+source(n.Slack.URL))
		case n.IncidentIO != nil:
			b.Notify = append(b.Notify, "incidentio, api key "+source(n.IncidentIO.APIKey))
		case n.PagerDuty != nil:
			b.Notify = append(b.Notify, "pagerduty as "+n.PagerDuty.From+", api key "+source(n.PagerDuty.APIKey))
		}
	}
	return b
}

// workerBanner is a worker's part of the file as its banner renders it:
// every secret as where it comes from, never as its value.
type workerBanner struct {
	Version  string
	Revision string
	Built    string
	Config   string
	LogLevel string

	Spooler string
	APIKey  string
	Queues  string

	Model    string
	ModelKey string
	Turns    string
	Task     string
	Tools    []string
	Report   string
}

func makeWorkerBanner(cfg *config, configPath string, logLevel slog.Level) workerBanner {
	spooler, _ := cfg.workerSpooler()
	b := workerBanner{
		Version:  stamp.Version(),
		Revision: stamp.Revision(),
		Built:    stamp.Built(),
		Config:   configPath,
		LogLevel: LogLevelVar(&logLevel).String(),
		Spooler:  spoolerLine(spooler),
		APIKey:   source(spooler.APIKey),
		Queues:   queuesLine(spooler),
	}
	b.Model, b.ModelKey, b.Turns, b.Task, b.Tools, b.Report = workerLines(&cfg.Worker)
	return b
}

// workerLines renders what a worker does, for either banner: the model
// with its defaults filled in and where its key comes from, the turns, the
// task, the tools and the report store.
func workerLines(w *workerConfig) (model, key, turns, task string, tools []string, report string) {
	switch m := w.Model; {
	case m.Claude != nil:
		model = modelLine("claude", cmp.Or(m.Claude.Name, string(claude.DefaultModel)), cmp.Or(m.Claude.MaxTokens, claude.DefaultMaxTokens))
	case m.OpenAI != nil:
		model = modelLine("openai", cmp.Or(m.OpenAI.Name, pagedog_openai.DefaultModel), cmp.Or(m.OpenAI.MaxTokens, pagedog_openai.DefaultMaxTokens))
	case m.Gemini != nil:
		model = modelLine("gemini", cmp.Or(m.Gemini.Name, gemini.DefaultModel), cmp.Or(m.Gemini.MaxTokens, gemini.DefaultMaxTokens))
	}
	key = modelKeyLine(w.Model)
	turns = strconv.Itoa(cmp.Or(w.MaxTurns, ai.DefaultMaxTurns))
	task = "built-in"
	switch {
	case w.Task.IsSecret():
		task = source(w.Task.Secret)
	case w.Task.Literal != "":
		task = "inline, " + strconv.Itoa(len(w.Task.Literal)) + " bytes"
	}
	if p := w.Tools.Prometheus; p != nil {
		tools = append(tools, "prometheus "+p.URL)
	}
	if l := w.Tools.Loki; l != nil {
		tools = append(tools, "loki "+l.URL)
	}
	for _, m := range w.Tools.MCP {
		tools = append(tools, mcpLine(&m))
	}
	switch r := w.Report; {
	case r.FS != nil:
		report = "fs " + r.FS.Dir
	case r.GitHub != nil:
		report = "github " + r.GitHub.Owner + "/" + r.GitHub.Repo + ", token " + source(r.GitHub.Token)
	}
	return model, key, turns, task, tools, report
}

// modelKeyLine says where a model's key comes from: the file's source, or
// the SDK's own variable, as set or unset, never as its value.
func modelKeyLine(m modelConfig) string {
	_, key, vars := modelKey(m)
	if !key.IsZero() {
		return source(key)
	}
	for _, name := range vars {
		if os.Getenv(name) != "" {
			return "env " + name + ", set"
		}
	}
	return "env " + strings.Join(vars, " or ") + ", unset"
}

// source names where a secret comes from, never what it is.
func source(s pagedog_config.Secret) string {
	switch {
	case s.Env != "":
		return "from env " + s.Env
	case s.File != "":
		return "from file " + s.File
	case s.Exec.Command != "":
		return "from exec " + s.Exec.Command
	}
	return "unset"
}

// value renders a configured value: a literal as itself, a secret as its
// source.
func value(v pagedog_config.Value) string {
	if v.IsSecret() {
		return source(v.Secret)
	}
	return v.Literal
}

func spoolerLine(s *spoolerConfig) string {
	line := s.Host
	if s.NoTLS {
		line += " (no tls)"
	}
	return line + ", spool " + s.Spool
}

func queuesLine(s *spoolerConfig) string {
	return s.Incidents + " -> " + s.Reports
}

func modelLine(kind, name string, maxTokens int) string {
	return fmt.Sprintf(
		"%s %s, max tokens %d",
		kind, name, maxTokens,
	)
}

// mcpLine renders an MCP server: a header as itself or, a secret, as its
// source; a program with its arguments and the variables it gets.
func mcpLine(m *mcpToolConfig) string {
	var sb strings.Builder
	sb.WriteString("mcp " + m.Name + " ")
	if m.URL != "" {
		sb.WriteString(m.URL)
		for _, name := range slices.Sorted(maps.Keys(m.Headers)) {
			sb.WriteString(", header " + name + " " + value(m.Headers[name]))
		}
	} else {
		sb.WriteString(strings.Join(append([]string{m.Command}, m.Args...), " "))
		if len(m.Env) > 0 {
			sb.WriteString(", env " + strings.Join(m.Env, ", "))
		}
	}
	if len(m.Tools) > 0 {
		sb.WriteString(", tools " + strings.Join(m.Tools, ", "))
	}
	return sb.String()
}
