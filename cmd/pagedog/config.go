package main

import (
	"errors"
	"fmt"
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

// runtimeConfig is how a worker is run; at most one field is set, and none
// means goroutines of this process. A worker that is pagedog reads this
// very file, as the server does: its worker section, and the Spooler the
// section names or, if empty, the file's.
type runtimeConfig struct {
	Exec   *execConfig   `yaml:"exec"`
	Docker *dockerConfig `yaml:"docker"`
}

// execConfig runs workers as child processes.
type execConfig struct {
	// Path is the program to run as a worker: a path, or a name looked up
	// in PATH, given Args. A worker that is pagedog is this binary run as
	// pagedog worker on a file.
	// Required.
	Path string `yaml:"path"`

	// Args are Path's arguments.
	Args []string `yaml:"args"`

	// Env is a worker's environment, and all of it: nothing of this
	// process's. A value is written as itself or, for a secret, as where it
	// comes from.
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

	// Command is what the image's entrypoint gets.
	//
	// Zero means the image's own command.
	Command []string `yaml:"command"`

	// Files are put into every container before it starts: each key an
	// absolute path in the container, its value a file of this process's
	// to read it from. How a worker gets a file when this process is a
	// container itself, with no host path to bind.
	Files map[string]string `yaml:"files"`

	// Env is a worker's environment, and all of it: nothing of this
	// process's. A value is written as itself or, for a secret, as where it
	// comes from.
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

// workerConfig is what a worker does with an incident. A worker that is
// pagedog reads it from this file, whatever its runtime; another program
// run as a worker never does.
type workerConfig struct {
	// Spooler is the Spooler server as a worker reaches it, and the key it
	// does so under.
	//
	// Zero means the file's spooler: the server's own.
	Spooler *spoolerConfig `yaml:"spooler"`

	// Model is the LLM the worker investigates with; exactly one field is
	// set.
	Model modelConfig `yaml:"model"`

	// MaxTurns is how many times the model is asked per incident before
	// the worker gives up.
	//
	// Zero means the worker's default.
	MaxTurns int `yaml:"max_turns"`

	// Task is the task given to the model, a Go text/template over the
	// incident: written as itself, or as where the worker reads it from,
	// the way a secret is.
	//
	// Zero means the worker's built-in task.
	Task pagedog_config.Value `yaml:"task"`

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
	// itself or, for a secret, as where it comes from, which the worker
	// reads.
	Headers map[string]pagedog_config.Value `yaml:"headers"`

	// Auth is how the worker authenticates to URL; if empty, the headers
	// alone do.
	Auth *mcpAuthConfig `yaml:"auth"`

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

// mcpAuthConfig is how a worker authenticates to an MCP server reached over
// HTTP: one scheme, with what it takes.
type mcpAuthConfig struct {
	// Bearer is the token sent as the Authorization header's bearer, read
	// by the worker.
	Bearer pagedog_config.Secret `yaml:"bearer"`
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

// openaiConfig is a model on the OpenAI Responses API.
type openaiConfig struct {
	// Name is the model's name.
	//
	// Zero means the worker's default.
	Name string `yaml:"name"`

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means the worker's default.
	MaxTokens int `yaml:"max_tokens"`

	// APIKey is the key the API is called under.
	//
	// Zero means the SDK's own: OPENAI_API_KEY of the worker's environment.
	APIKey pagedog_config.Secret `yaml:"api_key"`
}

// geminiConfig is a model on the Gemini Interactions API.
type geminiConfig struct {
	// Name is the model's name.
	//
	// Zero means the worker's default.
	Name string `yaml:"name"`

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means the worker's default.
	MaxTokens int `yaml:"max_tokens"`

	// APIKey is the key the API is called under.
	//
	// Zero means the SDK's own: GEMINI_API_KEY or GOOGLE_API_KEY of the
	// worker's environment.
	APIKey pagedog_config.Secret `yaml:"api_key"`
}

// claudeConfig is a Claude model on the Anthropic API.
type claudeConfig struct {
	// Name is the model's name.
	//
	// Zero means the worker's default.
	Name string `yaml:"name"`

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means the worker's default.
	MaxTokens int `yaml:"max_tokens"`

	// APIKey is the key the API is called under.
	//
	// Zero means the SDK's own: ANTHROPIC_API_KEY of the worker's
	// environment.
	APIKey pagedog_config.Secret `yaml:"api_key"`
}

// reportConfig is one report store; exactly one field is set.
type reportConfig struct {
	FS     *fsReportConfig     `yaml:"fs"`
	GitHub *githubReportConfig `yaml:"github"`
}

// fsReportConfig stores reports as files, one per incident.
type fsReportConfig struct {
	// Dir is the directory. The workers and this process must see the same
	// place under it: the working directory for exec workers, one volume
	// mounted at the same path for docker ones.
	Dir string `yaml:"dir"`
}

// githubReportConfig stores reports as issues of a repository, one per
// incident, the same incident updating its issue.
type githubReportConfig struct {
	Owner string `yaml:"owner"`
	Repo  string `yaml:"repo"`

	// Token is the API token the issues are made under, with write access
	// to them.
	Token pagedog_config.Secret `yaml:"token"`
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

// parseWorkerConfig reads a configuration file's contents as a worker does:
// its worker section and the Spooler it reaches are checked, the rest is
// the server's business.
func parseWorkerConfig(data []byte) (config, error) {
	var c config
	if err := pagedog_config.Unmarshal(data, &c); err != nil {
		return config{}, err
	}
	spooler, path := c.workerSpooler()
	err := errors.Join(
		spooler.validate(path),
		c.Worker.validate("worker"),
	)
	if err != nil {
		return config{}, err
	}
	return c, nil
}

// workerSpooler is the Spooler a worker reaches, with its path in the file:
// the worker's own, or the file's.
func (c *config) workerSpooler() (*spoolerConfig, string) {
	if c.Worker.Spooler != nil {
		return c.Worker.Spooler, "worker.spooler"
	}
	return &c.Spooler, "spooler"
}

func (c *config) validate() error {
	var errs []error
	errs = append(errs,
		c.Spooler.validate("spooler"),
		required("webhook.listen", c.Webhook.Listen != ""),
	)
	for i, s := range c.Webhook.Sources {
		errs = append(errs, s.validate(fmt.Sprintf("webhook.sources[%d]", i)))
	}
	errs = append(errs,
		c.Workers.Runtime.validate("workers.runtime"),
		c.Worker.validate("worker"),
	)
	if s := c.Worker.Spooler; s != nil {
		errs = append(errs, s.validate("worker.spooler"))
	}
	for i, n := range c.Notify {
		errs = append(errs, n.validate(fmt.Sprintf("notify[%d]", i)))
	}
	return errors.Join(errs...)
}

func (s *spoolerConfig) validate(path string) error {
	return errors.Join(
		required(path+".host", s.Host != ""),
		required(path+".api_key", !s.APIKey.IsZero()),
		required(path+".spool", s.Spool != ""),
		required(path+".incidents", s.Incidents != ""),
		required(path+".reports", s.Reports != ""),
	)
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
	err := atMostOne(path, []field{
		{"exec", r.Exec != nil},
		{"docker", r.Docker != nil},
	})
	if err != nil {
		return err
	}
	switch {
	case r.Exec != nil:
		return required(path+".exec.path", r.Exec.Path != "")
	case r.Docker != nil:
		errs := []error{
			required(path+".docker.image", r.Docker.Image != ""),
		}
		for dst, src := range r.Docker.Files {
			switch {
			case !strings.HasPrefix(dst, "/"):
				errs = append(errs, fmt.Errorf(
					"%s.docker.files: %s: an absolute path in the container is required",
					path, dst,
				))
			case src == "":
				errs = append(errs, fmt.Errorf(
					"%s.docker.files: %s: the file to read is required",
					path, dst,
				))
			}
		}
		return errors.Join(errs...)
	}
	return nil
}

func (w *workerConfig) validate(path string) error {
	return errors.Join(
		w.Model.validate(path+".model"),
		w.Tools.validate(path+".tools"),
		w.Report.validate(path+".report"),
	)
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
	if m.Auth != nil {
		switch {
		case m.URL == "":
			errs = append(errs, fmt.Errorf(
				"%s.auth: only for a url",
				path,
			))
		default:
			errs = append(errs, required(path+".auth.bearer", !m.Auth.Bearer.IsZero()))
			for name := range m.Headers {
				if strings.EqualFold(name, "Authorization") {
					errs = append(errs, fmt.Errorf(
						"%s.headers.%s: auth sets it",
						path, name,
					))
				}
			}
		}
	}
	return errors.Join(errs...)
}

func (r *reportConfig) validate(path string) error {
	err := oneOf(path, []field{
		{"fs", r.FS != nil},
		{"github", r.GitHub != nil},
	})
	if err != nil {
		return err
	}
	switch {
	case r.FS != nil:
		return required(path+".fs.dir", r.FS.Dir != "")
	case r.GitHub != nil:
		return errors.Join(
			required(path+".github.owner", r.GitHub.Owner != ""),
			required(path+".github.repo", r.GitHub.Repo != ""),
			required(path+".github.token", !r.GitHub.Token.IsZero()),
		)
	}
	return nil
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
	names, set := setOf(fields)
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

// atMostOne checks that no two fields are set; none is fine.
func atMostOne(path string, fields []field) error {
	names, set := setOf(fields)
	if len(set) > 1 {
		return fmt.Errorf(
			"%s: only one of %s is allowed, not %s",
			path, strings.Join(names, ", "), strings.Join(set, " and "),
		)
	}
	return nil
}

// setOf names the fields, and those of them set.
func setOf(fields []field) (names, set []string) {
	for _, f := range fields {
		names = append(names, f.name)
		if f.set {
			set = append(set, f.name)
		}
	}
	return names, set
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
