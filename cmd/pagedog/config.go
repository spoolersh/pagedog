package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	pagedog_config "github.com/spoolersh/pagedog/internal/config"
)

// config is the configuration file.
type config struct {
	Spooler spoolerConfig    `yaml:"spooler"`
	Webhook webhookConfig    `yaml:"webhook"`
	Workers workersConfig    `yaml:"workers"`
	Worker  workerConfig     `yaml:"worker"`
	Notify  []notifierConfig `yaml:"notify"`
}

// spoolerConfig is the Spooler server and the two queues on it.
type spoolerConfig struct {
	// Host is the API's host:port.
	Host string `yaml:"host"`

	// APIKey authenticates every request.
	APIKey pagedog_config.Secret `yaml:"api_key"`

	// NoTLS talks plain HTTP: local runs only.
	NoTLS bool `yaml:"no_tls"`

	// Spool is the spool the queues are in.
	Spool string `yaml:"spool"`

	// Incidents is the queue incidents go to and the workers take them from.
	// It exists already: pagedog creates no queue.
	Incidents string `yaml:"incidents"`

	// Reports is the queue the workers put their reports on. It exists
	// already too.
	Reports string `yaml:"reports"`
}

// webhookConfig is the HTTP server incident notifications come to.
type webhookConfig struct {
	// Listen is the host:port to bind to.
	Listen string `yaml:"listen"`

	// Sources are the notifications accepted, each mounted under its name.
	Sources []sourceConfig `yaml:"sources"`
}

// sourceConfig is one incident source; exactly one field is set.
type sourceConfig struct {
	Alertmanager *alertmanagerConfig `yaml:"alertmanager"`
	IncidentIO   *incidentioConfig   `yaml:"incidentio"`
	PagerDuty    *pagerdutyConfig    `yaml:"pagerduty"`
}

// alertmanagerConfig accepts Alertmanager's webhook notifications.
type alertmanagerConfig struct {
	// Token is the bearer token every notification must carry.
	Token pagedog_config.Secret `yaml:"token"`
}

// incidentioConfig accepts incident.io's webhook events.
type incidentioConfig struct {
	// SigningSecret is the webhook endpoint's signing secret, as incident.io
	// shows it, which every event must be signed under.
	SigningSecret pagedog_config.Secret `yaml:"signing_secret"`
}

// pagerdutyConfig accepts PagerDuty's v3 webhook events.
type pagerdutyConfig struct {
	// Secret is the webhook subscription's secret, as PagerDuty showed it
	// when the subscription was made, which every event must be signed
	// under.
	Secret pagedog_config.Secret `yaml:"secret"`
}

// workersConfig is how many workers run, and how.
type workersConfig struct {
	// Min is how many run even with nothing to do.
	//
	// Zero means none.
	Min int `yaml:"min"`

	// Max is how many run at most.
	//
	// Zero means the reconciler's default.
	Max int `yaml:"max"`

	Runtime runtimeConfig `yaml:"runtime"`
}

// runtimeConfig is how a worker is run; exactly one field is set.
type runtimeConfig struct {
	Exec   *execConfig   `yaml:"exec"`
	Docker *dockerConfig `yaml:"docker"`
}

// workerSpoolerAPIKey is the variable a worker takes its Spooler API key
// from: every runtime's env sets it, as a secret.
const workerSpoolerAPIKey = "PAGEDOG_SPOOLER_API_KEY"

// execConfig runs workers as child processes.
type execConfig struct {
	// Path is the program to run as a worker: a path, or a name looked up
	// in PATH. It is given the worker command and its flags.
	//
	// Zero means this program.
	Path string `yaml:"path"`

	// Env is a worker's environment, and all of it: nothing of this
	// process's. A value is written as itself or, for a secret, as where it
	// comes from; PAGEDOG_SPOOLER_API_KEY, the worker's Spooler API key, is
	// one.
	Env map[string]pagedog_config.Value `yaml:"env"`

	// StopTimeout is how long a worker has to exit after SIGTERM before it
	// is killed.
	//
	// Zero means the runtime's default.
	StopTimeout time.Duration `yaml:"stop_timeout"`
}

// dockerConfig runs workers as containers, through the Docker engine the
// environment points to.
type dockerConfig struct {
	// Image is the worker image.
	Image string `yaml:"image"`

	// Env is a worker's environment, and all of it: nothing of this
	// process's. A value is written as itself or, for a secret, as where it
	// comes from; PAGEDOG_SPOOLER_API_KEY, the worker's Spooler API key, is
	// one.
	Env map[string]pagedog_config.Value `yaml:"env"`

	// Network is the network a worker joins, such as a compose project's.
	//
	// Zero means the engine's default.
	Network string `yaml:"network"`

	// Binds are volumes to mount, as "volume-or-path:/container/path".
	Binds []string `yaml:"binds"`

	// StopTimeout is how long a worker has to exit after a stop before it
	// is killed.
	//
	// Zero means the runtime's default.
	StopTimeout time.Duration `yaml:"stop_timeout"`
}

// workerConfig is what a worker does with an incident; the launcher passes
// it to every worker it runs.
type workerConfig struct {
	// Model is the LLM the worker investigates with; exactly one field is
	// set.
	Model modelConfig `yaml:"model"`

	// MaxTurns is how many times the model is asked per incident before
	// the worker gives up.
	//
	// Zero means the worker's default.
	MaxTurns int `yaml:"max_turns"`

	// Task is the task given to the model, a Go text/template over the
	// incident; TaskFile is a file holding it, read by this process. At
	// most one is set.
	//
	// Zero means the worker's built-in task.
	Task     string `yaml:"task"`
	TaskFile string `yaml:"task_file"`

	// Tools is what the model may look at.
	Tools toolsConfig `yaml:"tools"`

	// Report is where the workers store their reports, and where the
	// notifiers read them back from; exactly one field is set.
	Report reportConfig `yaml:"report"`
}

// toolsConfig is the backends the model may look at; any may be left out.
type toolsConfig struct {
	Prometheus *prometheusToolConfig `yaml:"prometheus"`
	Loki       *lokiToolConfig       `yaml:"loki"`

	// MCP is MCP servers, whose tools the model gets as the servers
	// describe them.
	MCP []mcpToolConfig `yaml:"mcp"`
}

// mcpToolConfig is one MCP server, reached over HTTP or run by the worker as
// a child process; exactly one of URL and Command is set.
type mcpToolConfig struct {
	// Name names the server in errors and logs.
	Name string `yaml:"name"`

	// URL is the server's endpoint, for one reached over HTTP.
	URL string `yaml:"url"`

	// Headers are set on every request to URL. A header is written as
	// itself or, for a secret, as {env: NAME}: the variable of the worker's
	// environment that holds it, as Env names them.
	Headers map[string]pagedog_config.Value `yaml:"headers"`

	// Command is the server's program, for one the worker runs: a path in
	// the worker's image, or a name on its PATH.
	Command string `yaml:"command"`

	// Args are the command's arguments.
	Args []string `yaml:"args"`

	// Env names the variables of the worker's environment the command gets,
	// which is all it gets.
	Env []string `yaml:"env"`

	// Tools names the tools offered to the model, of all the server has.
	//
	// Zero means all of them.
	Tools []string `yaml:"tools"`
}

// prometheusToolConfig reads metrics from a Prometheus server.
type prometheusToolConfig struct {
	// URL is the server's base URL, as a worker reaches it.
	URL string `yaml:"url"`
}

// lokiToolConfig reads logs from a Loki server.
type lokiToolConfig struct {
	// URL is the server's base URL, as a worker reaches it.
	URL string `yaml:"url"`
}

// modelConfig is one LLM; exactly one field is set.
type modelConfig struct {
	Claude *claudeConfig `yaml:"claude"`
	OpenAI *openaiConfig `yaml:"openai"`
	Gemini *geminiConfig `yaml:"gemini"`
}

// openaiConfig is a model on the OpenAI Responses API, whose key a worker
// takes from its environment as OPENAI_API_KEY.
type openaiConfig struct {
	// Name is the model's name.
	//
	// Zero means the worker's default.
	Name string `yaml:"name"`

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means the worker's default.
	MaxTokens int `yaml:"max_tokens"`
}

// geminiConfig is a model on the Gemini Interactions API, whose key a
// worker takes from its environment as GEMINI_API_KEY.
type geminiConfig struct {
	// Name is the model's name.
	//
	// Zero means the worker's default.
	Name string `yaml:"name"`

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means the worker's default.
	MaxTokens int `yaml:"max_tokens"`
}

// claudeConfig is a Claude model on the Anthropic API, whose key a worker
// takes from its environment as ANTHROPIC_API_KEY.
type claudeConfig struct {
	// Name is the model's name.
	//
	// Zero means the worker's default.
	Name string `yaml:"name"`

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means the worker's default.
	MaxTokens int `yaml:"max_tokens"`
}

// reportConfig is one report store; exactly one field is set.
type reportConfig struct {
	FS *fsReportConfig `yaml:"fs"`
}

// fsReportConfig stores reports as files, one per incident.
type fsReportConfig struct {
	// Dir is the directory. The workers and this process must see the same
	// place under it: the working directory for exec workers, one volume
	// mounted at the same path for docker ones.
	Dir string `yaml:"dir"`
}

// notifierConfig is one place reports go; exactly one field is set.
type notifierConfig struct {
	Log        *logNotifierConfig        `yaml:"log"`
	Webhook    *webhookNotifierConfig    `yaml:"webhook"`
	Slack      *slackNotifierConfig      `yaml:"slack"`
	IncidentIO *incidentioNotifierConfig `yaml:"incidentio"`
	PagerDuty  *pagerdutyNotifierConfig  `yaml:"pagerduty"`
}

// pagerdutyNotifierConfig shares a report on its incident in PagerDuty, as
// a note; reports on incidents from other sources are left alone.
type pagerdutyNotifierConfig struct {
	// APIKey authenticates to the API: a REST API key, or a user's.
	APIKey pagedog_config.Secret `yaml:"api_key"`

	// From is the email address of a user of the account, which the API
	// wants every note to be from.
	From string `yaml:"from"`

	// URL is the API's base URL.
	//
	// Zero means PagerDuty's, in the US region.
	URL string `yaml:"url"`
}

// incidentioNotifierConfig shares a report on its incident in incident.io,
// as an update; reports on incidents from other sources are left alone.
type incidentioNotifierConfig struct {
	// APIKey authenticates to the API, with the incident_updates.create
	// scope.
	APIKey pagedog_config.Secret `yaml:"api_key"`

	// URL is the API's base URL.
	//
	// Zero means incident.io's.
	URL string `yaml:"url"`
}

// logNotifierConfig writes reports to the log.
type logNotifierConfig struct{}

// webhookNotifierConfig posts reports to a URL as JSON.
type webhookNotifierConfig struct {
	URL string `yaml:"url"`

	// Token is the bearer token to send.
	//
	// Zero means none.
	Token pagedog_config.Secret `yaml:"token"`
}

// slackNotifierConfig posts reports to a Slack incoming webhook.
type slackNotifierConfig struct {
	// URL is the incoming webhook's URL, which is a secret.
	URL pagedog_config.Secret `yaml:"url"`
}

// parseConfig reads a configuration file's contents and checks them.
func parseConfig(data []byte) (config, error) {
	var c config
	if err := pagedog_config.Unmarshal(data, &c); err != nil {
		return config{}, err
	}
	if err := c.validate(); err != nil {
		return config{}, err
	}
	return c, nil
}

func (c *config) validate() error {
	var errs []error
	errs = append(errs,
		required("spooler.host", c.Spooler.Host != ""),
		required("spooler.api_key", !c.Spooler.APIKey.IsZero()),
		required("spooler.spool", c.Spooler.Spool != ""),
		required("spooler.incidents", c.Spooler.Incidents != ""),
		required("spooler.reports", c.Spooler.Reports != ""),
		required("webhook.listen", c.Webhook.Listen != ""),
	)
	for i, s := range c.Webhook.Sources {
		errs = append(errs, s.validate(fmt.Sprintf("webhook.sources[%d]", i)))
	}
	errs = append(errs,
		c.Workers.Runtime.validate("workers.runtime"),
		c.Worker.validate("worker"),
	)
	for i, n := range c.Notify {
		errs = append(errs, n.validate(fmt.Sprintf("notify[%d]", i)))
	}
	return errors.Join(errs...)
}

func (s *sourceConfig) validate(path string) error {
	err := oneOf(path, []field{
		{"alertmanager", s.Alertmanager != nil},
		{"incidentio", s.IncidentIO != nil},
		{"pagerduty", s.PagerDuty != nil},
	})
	if err != nil {
		return err
	}
	switch {
	case s.Alertmanager != nil:
		return required(path+".alertmanager.token", !s.Alertmanager.Token.IsZero())
	case s.IncidentIO != nil:
		return required(path+".incidentio.signing_secret", !s.IncidentIO.SigningSecret.IsZero())
	case s.PagerDuty != nil:
		return required(path+".pagerduty.secret", !s.PagerDuty.Secret.IsZero())
	}
	return nil
}

func (r *runtimeConfig) validate(path string) error {
	err := oneOf(path, []field{
		{"exec", r.Exec != nil},
		{"docker", r.Docker != nil},
	})
	if err != nil {
		return err
	}
	switch {
	case r.Exec != nil:
		return requiredSecret(path+".exec.env", r.Exec.Env)
	case r.Docker != nil:
		return errors.Join(
			required(path+".docker.image", r.Docker.Image != ""),
			requiredSecret(path+".docker.env", r.Docker.Env),
		)
	}
	return nil
}

// requiredSecret checks that a worker's environment sets its Spooler API
// key, and as a secret: the file never holds it.
func requiredSecret(path string, env map[string]pagedog_config.Value) error {
	path += "." + workerSpoolerAPIKey
	v, has := env[workerSpoolerAPIKey]
	if !has {
		return required(path, false)
	}
	if !v.IsSecret() {
		return fmt.Errorf(
			"%s: a secret is written as where it comes from, not as its value",
			path,
		)
	}
	return nil
}

func (w *workerConfig) validate(path string) error {
	var errs []error
	if w.Task != "" && w.TaskFile != "" {
		errs = append(errs, fmt.Errorf(
			"%s: only one of task, task_file is allowed, not both",
			path,
		))
	}
	return errors.Join(append(errs,
		w.Model.validate(path+".model"),
		w.Tools.validate(path+".tools"),
		w.Report.validate(path+".report"),
	)...)
}

func (m *modelConfig) validate(path string) error {
	return oneOf(path, []field{
		{"claude", m.Claude != nil},
		{"openai", m.OpenAI != nil},
		{"gemini", m.Gemini != nil},
	})
}

func (t *toolsConfig) validate(path string) error {
	var errs []error
	if t.Prometheus != nil {
		errs = append(errs, required(path+".prometheus.url", t.Prometheus.URL != ""))
	}
	if t.Loki != nil {
		errs = append(errs, required(path+".loki.url", t.Loki.URL != ""))
	}
	for i, m := range t.MCP {
		errs = append(errs, m.validate(fmt.Sprintf("%s.mcp[%d]", path, i)))
	}
	return errors.Join(errs...)
}

func (m *mcpToolConfig) validate(path string) error {
	errs := []error{
		required(path+".name", m.Name != ""),
		oneOf(path, []field{
			{"url", m.URL != ""},
			{"command", m.Command != ""},
		}),
	}
	if len(m.Headers) > 0 && m.URL == "" {
		errs = append(errs, fmt.Errorf(
			"%s.headers: only for a url",
			path,
		))
	}
	// A secret header is read by the worker, from its environment: the
	// launcher never puts one on a command line.
	for _, name := range slices.Sorted(maps.Keys(m.Headers)) {
		s := m.Headers[name].Secret
		if !s.IsZero() && (s.Env == "" || s.File != "" || s.Exec.Command != "") {
			errs = append(errs, fmt.Errorf(
				"%s.headers.%s: a secret header is {env: NAME}, a variable of the worker's environment",
				path, name,
			))
		}
	}
	return errors.Join(errs...)
}

func (r *reportConfig) validate(path string) error {
	err := oneOf(path, []field{
		{"fs", r.FS != nil},
	})
	if err != nil {
		return err
	}
	return required(path+".fs.dir", r.FS.Dir != "")
}

func (n *notifierConfig) validate(path string) error {
	err := oneOf(path, []field{
		{"log", n.Log != nil},
		{"webhook", n.Webhook != nil},
		{"slack", n.Slack != nil},
		{"incidentio", n.IncidentIO != nil},
		{"pagerduty", n.PagerDuty != nil},
	})
	if err != nil {
		return err
	}
	switch {
	case n.Webhook != nil:
		return required(path+".webhook.url", n.Webhook.URL != "")
	case n.Slack != nil:
		return required(path+".slack.url", !n.Slack.URL.IsZero())
	case n.IncidentIO != nil:
		return required(path+".incidentio.api_key", !n.IncidentIO.APIKey.IsZero())
	case n.PagerDuty != nil:
		return errors.Join(
			required(path+".pagerduty.api_key", !n.PagerDuty.APIKey.IsZero()),
			required(path+".pagerduty.from", n.PagerDuty.From != ""),
		)
	}
	return nil
}

// field is one of a one-of's choices, and whether it is set.
type field struct {
	name string
	set  bool
}

// oneOf checks that exactly one field is set.
func oneOf(path string, fields []field) error {
	var (
		names []string
		set   []string
	)
	for _, f := range fields {
		names = append(names, f.name)
		if f.set {
			set = append(set, f.name)
		}
	}
	switch len(set) {
	case 1:
		return nil
	case 0:
		return fmt.Errorf(
			"%s: one of %s is required",
			path, strings.Join(names, ", "),
		)
	default:
		return fmt.Errorf(
			"%s: only one of %s is allowed, not %s",
			path, strings.Join(names, ", "), strings.Join(set, " and "),
		)
	}
}

func required(path string, has bool) error {
	if !has {
		return fmt.Errorf(
			"%s: required",
			path,
		)
	}
	return nil
}
