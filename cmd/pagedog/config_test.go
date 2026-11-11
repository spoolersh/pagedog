package main

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	pagedog_config "github.com/spoolersh/pagedog/internal/config"
)

func TestParseConfig(t *testing.T) {
	const full = `
spooler:
  host: 127.0.0.1:8080
  api_key: {env: TEST_SPOOLER_API_KEY}
  no_tls: true
  spool: default
  incidents: incidents
  reports: reports

webhook:
  listen: 127.0.0.1:7070
  sources:
    - alertmanager:
        token: {env: TEST_ALERTMANAGER_TOKEN}
    - incidentio:
        signing_secret: {env: TEST_INCIDENTIO_SECRET}
    - pagerduty:
        secret: {env: TEST_PAGERDUTY_SECRET}

workers:
  min: 1
  max: 3
  runtime:
    exec:
      path: worker
      env:
        TZ: UTC
        PAGEDOG_SPOOLER_API_KEY: {env: TEST_SPOOLER_API_KEY}
        ANTHROPIC_API_KEY: {env: TEST_ANTHROPIC_API_KEY}
      stop_timeout: 10s

worker:
  model:
    claude:
      name: claude-opus-5-5
      max_tokens: 4096
  max_turns: 10
  task_file: prompts/task.md
  tools:
    prometheus:
      url: http://prometheus:9090
    loki:
      url: http://loki:3100
    mcp:
      - name: prometheus
        url: http://prometheus-mcp:8080/mcp
        headers:
          X-Scope-OrgID: tenant-1
          Authorization: {env: PROMETHEUS_MCP_TOKEN}
        tools: [query, range_query]
      - name: inventory
        command: /usr/lib/pagedog/inventory-tool
        args: [--verbose]
        env: [HOME]
  report:
    fs:
      dir: /reports

notify:
  - log: {}
  - webhook:
      url: http://demoapp:8080/report
      token: {env: TEST_WEBHOOK_TOKEN}
  - slack:
      url: {env: TEST_SLACK_URL}
  - incidentio:
      api_key: {env: TEST_INCIDENTIO_API_KEY}
  - pagerduty:
      api_key: {env: TEST_PAGERDUTY_API_KEY}
      from: pagedog@example.com
      url: https://api.eu.pagerduty.com
`
	act, err := parseConfig([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	exp := config{
		Spooler: spoolerConfig{
			Host: "127.0.0.1:8080",
			APIKey: pagedog_config.Secret{
				Env: "TEST_SPOOLER_API_KEY",
			},
			NoTLS:     true,
			Spool:     "default",
			Incidents: "incidents",
			Reports:   "reports",
		},
		Webhook: webhookConfig{
			Listen: "127.0.0.1:7070",
			Sources: []sourceConfig{
				{
					Alertmanager: &alertmanagerConfig{
						Token: pagedog_config.Secret{
							Env: "TEST_ALERTMANAGER_TOKEN",
						},
					},
				},
				{
					IncidentIO: &incidentioConfig{
						SigningSecret: pagedog_config.Secret{
							Env: "TEST_INCIDENTIO_SECRET",
						},
					},
				},
				{
					PagerDuty: &pagerdutyConfig{
						Secret: pagedog_config.Secret{
							Env: "TEST_PAGERDUTY_SECRET",
						},
					},
				},
			},
		},
		Workers: workersConfig{
			Min: 1,
			Max: 3,
			Runtime: runtimeConfig{
				Exec: &execConfig{
					Path: "worker",
					Env: map[string]pagedog_config.Value{
						"TZ": {
							Literal: "UTC",
						},
						"PAGEDOG_SPOOLER_API_KEY": {
							Secret: pagedog_config.Secret{
								Env: "TEST_SPOOLER_API_KEY",
							},
						},
						"ANTHROPIC_API_KEY": {
							Secret: pagedog_config.Secret{
								Env: "TEST_ANTHROPIC_API_KEY",
							},
						},
					},
					StopTimeout: 10 * time.Second,
				},
			},
		},
		Worker: workerConfig{
			Model: modelConfig{
				Claude: &claudeConfig{
					Name:      "claude-opus-5-5",
					MaxTokens: 4096,
				},
			},
			MaxTurns: 10,
			TaskFile: "prompts/task.md",
			Tools: toolsConfig{
				Prometheus: &prometheusToolConfig{
					URL: "http://prometheus:9090",
				},
				Loki: &lokiToolConfig{
					URL: "http://loki:3100",
				},
				MCP: []mcpToolConfig{
					{
						Name: "prometheus",
						URL:  "http://prometheus-mcp:8080/mcp",
						Headers: map[string]pagedog_config.Value{
							"X-Scope-OrgID": {
								Literal: "tenant-1",
							},
							"Authorization": {
								Secret: pagedog_config.Secret{
									Env: "PROMETHEUS_MCP_TOKEN",
								},
							},
						},
						Tools: []string{"query", "range_query"},
					},
					{
						Name:    "inventory",
						Command: "/usr/lib/pagedog/inventory-tool",
						Args:    []string{"--verbose"},
						Env:     []string{"HOME"},
					},
				},
			},
			Report: reportConfig{
				FS: &fsReportConfig{
					Dir: "/reports",
				},
			},
		},
		Notify: []notifierConfig{
			{
				Log: &logNotifierConfig{},
			},
			{
				Webhook: &webhookNotifierConfig{
					URL: "http://demoapp:8080/report",
					Token: pagedog_config.Secret{
						Env: "TEST_WEBHOOK_TOKEN",
					},
				},
			},
			{
				Slack: &slackNotifierConfig{
					URL: pagedog_config.Secret{
						Env: "TEST_SLACK_URL",
					},
				},
			},
			{
				IncidentIO: &incidentioNotifierConfig{
					APIKey: pagedog_config.Secret{
						Env: "TEST_INCIDENTIO_API_KEY",
					},
				},
			},
			{
				PagerDuty: &pagerdutyNotifierConfig{
					APIKey: pagedog_config.Secret{
						Env: "TEST_PAGERDUTY_API_KEY",
					},
					From: "pagedog@example.com",
					URL:  "https://api.eu.pagerduty.com",
				},
			},
		},
	}
	if !cmp.Equal(exp, act) {
		t.Errorf(
			"config: %s",
			cmp.Diff(exp, act),
		)
	}
}

// minimal is a file that parses, with an exec runtime.
const minimal = `
spooler:
  host: 127.0.0.1:8080
  api_key: {env: TEST_SPOOLER_API_KEY}
  spool: default
  incidents: incidents
  reports: reports
webhook:
  listen: 127.0.0.1:7070
workers:
  runtime:
    exec:
      path: worker
      env:
        PAGEDOG_SPOOLER_API_KEY: {env: TEST_SPOOLER_API_KEY}
worker:
  model:
    claude: {}
  report:
    fs:
      dir: reports
`

// minimalExec is the runtime block of minimal.
const minimalExec = "    exec:\n      path: worker\n      env:\n        PAGEDOG_SPOOLER_API_KEY: {env: TEST_SPOOLER_API_KEY}\n"

func TestParseConfigDocker(t *testing.T) {
	act, err := parseConfig([]byte(strings.Replace(minimal, minimalExec, `
    docker:
      image: pagedog-worker
      env:
        PAGEDOG_SPOOLER_API_KEY: {env: TEST_SPOOLER_API_KEY}
        ANTHROPIC_API_KEY: {env: TEST_ANTHROPIC_API_KEY}
      network: pagedog_default
      binds:
        - pagedog_reports:/reports
      stop_timeout: 10s
`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	exp := runtimeConfig{
		Docker: &dockerConfig{
			Image: "pagedog-worker",
			Env: map[string]pagedog_config.Value{
				"PAGEDOG_SPOOLER_API_KEY": {
					Secret: pagedog_config.Secret{
						Env: "TEST_SPOOLER_API_KEY",
					},
				},
				"ANTHROPIC_API_KEY": {
					Secret: pagedog_config.Secret{
						Env: "TEST_ANTHROPIC_API_KEY",
					},
				},
			},
			Network: "pagedog_default",
			Binds: []string{
				"pagedog_reports:/reports",
			},
			StopTimeout: 10 * time.Second,
		},
	}
	if act := act.Workers.Runtime; !cmp.Equal(exp, act) {
		t.Errorf(
			"runtime: %s",
			cmp.Diff(exp, act),
		)
	}
}

func TestParseConfigErrors(t *testing.T) {
	// Each case breaks minimal one way.
	if _, err := parseConfig([]byte(minimal)); err != nil {
		t.Fatalf("parseConfig(minimal): %v", err)
	}
	for _, tc := range []struct {
		name string
		yaml string

		// exp is a part of the error's text.
		exp string
	}{
		{
			name: "missing host",
			yaml: strings.Replace(minimal, "host: 127.0.0.1:8080", "", 1),
			exp:  "spooler.host: required",
		},
		{
			name: "alertmanager without a token",
			yaml: strings.Replace(minimal, "listen: 127.0.0.1:7070", "listen: 127.0.0.1:7070\n  sources:\n    - alertmanager: {}", 1),
			exp:  "webhook.sources[0].alertmanager.token: required",
		},
		{
			name: "incidentio without a signing secret",
			yaml: strings.Replace(minimal, "listen: 127.0.0.1:7070", "listen: 127.0.0.1:7070\n  sources:\n    - incidentio: {}", 1),
			exp:  "webhook.sources[0].incidentio.signing_secret: required",
		},
		{
			name: "pagerduty without a secret",
			yaml: strings.Replace(minimal, "listen: 127.0.0.1:7070", "listen: 127.0.0.1:7070\n  sources:\n    - pagerduty: {}", 1),
			exp:  "webhook.sources[0].pagerduty.secret: required",
		},
		{
			name: "two sources in one",
			yaml: strings.Replace(minimal, "listen: 127.0.0.1:7070", "listen: 127.0.0.1:7070\n  sources:\n    - alertmanager: {}\n      incidentio: {}", 1),
			exp:  "webhook.sources[0]: only one of alertmanager, incidentio, pagerduty is allowed, not alertmanager and incidentio",
		},
		{
			name: "no runtime",
			yaml: strings.Replace(minimal, minimalExec, "    {}\n", 1),
			exp:  "workers.runtime: one of exec, docker is required",
		},
		{
			name: "exec without the worker's spooler key",
			yaml: strings.Replace(minimal, minimalExec, "    exec:\n      path: worker\n", 1),
			exp:  "workers.runtime.exec.env.PAGEDOG_SPOOLER_API_KEY: required",
		},
		{
			name: "the worker's spooler key written as its value",
			yaml: strings.Replace(minimal, minimalExec, "    exec:\n      path: worker\n      env:\n        PAGEDOG_SPOOLER_API_KEY: local\n", 1),
			exp:  "workers.runtime.exec.env.PAGEDOG_SPOOLER_API_KEY: a secret is written as where it comes from, not as its value",
		},
		{
			name: "two runtimes",
			yaml: strings.Replace(minimal, minimalExec, minimalExec+"    docker:\n      image: pagedog-worker\n", 1),
			exp:  "workers.runtime: only one of exec, docker is allowed, not exec and docker",
		},
		{
			name: "docker without an image",
			yaml: strings.Replace(minimal, minimalExec, "    docker: {}\n", 1),
			exp:  "workers.runtime.docker.image: required",
		},
		{
			name: "no model",
			yaml: strings.Replace(minimal, "    claude: {}\n", "    {}\n", 1),
			exp:  "worker.model: one of claude, openai, gemini is required",
		},
		{
			name: "prometheus tool without a url",
			yaml: strings.Replace(minimal, "  report:\n", "  tools:\n    prometheus: {}\n  report:\n", 1),
			exp:  "worker.tools.prometheus.url: required",
		},
		{
			name: "task written and in a file",
			yaml: strings.Replace(minimal, "  report:\n", "  task: x\n  task_file: y\n  report:\n", 1),
			exp:  "worker: only one of task, task_file is allowed, not both",
		},
		{
			name: "mcp server without a name",
			yaml: strings.Replace(minimal, "  report:\n", "  tools:\n    mcp:\n      - url: http://x/mcp\n  report:\n", 1),
			exp:  "worker.tools.mcp[0].name: required",
		},
		{
			name: "mcp server from nowhere",
			yaml: strings.Replace(minimal, "  report:\n", "  tools:\n    mcp:\n      - name: x\n  report:\n", 1),
			exp:  "worker.tools.mcp[0]: one of url, command is required",
		},
		{
			name: "mcp server with a url and a command",
			yaml: strings.Replace(minimal, "  report:\n", "  tools:\n    mcp:\n      - name: x\n        url: http://x/mcp\n        command: x\n  report:\n", 1),
			exp:  "worker.tools.mcp[0]: only one of url, command is allowed, not url and command",
		},
		{
			name: "mcp headers on a command",
			yaml: strings.Replace(minimal, "  report:\n", "  tools:\n    mcp:\n      - name: x\n        command: x\n        headers: {A: b}\n  report:\n", 1),
			exp:  "worker.tools.mcp[0].headers: only for a url",
		},
		{
			name: "mcp header from a file",
			yaml: strings.Replace(minimal, "  report:\n", "  tools:\n    mcp:\n      - name: x\n        url: http://x/mcp\n        headers: {A: {file: /x}}\n  report:\n", 1),
			exp:  "worker.tools.mcp[0].headers.A: a secret header is {env: NAME}, a variable of the worker's environment",
		},
		{
			name: "report store without a directory",
			yaml: strings.Replace(minimal, "    fs:\n      dir: reports\n", "    fs: {}\n", 1),
			exp:  "worker.report.fs.dir: required",
		},
		{
			name: "two notifiers in one",
			yaml: minimal + "\nnotify:\n  - log: {}\n    webhook:\n      url: http://x\n",
			exp:  "notify[0]: only one of log, webhook, slack, incidentio, pagerduty is allowed, not log and webhook",
		},
		{
			name: "empty notifier",
			yaml: minimal + "\nnotify:\n  - {}\n",
			exp:  "notify[0]: one of log, webhook, slack, incidentio, pagerduty is required",
		},
		{
			name: "pagerduty notifier without a sender",
			yaml: minimal + "\nnotify:\n  - pagerduty:\n      api_key: {env: X}\n",
			exp:  "notify[0].pagerduty.from: required",
		},
		{
			name: "webhook without url",
			yaml: minimal + "\nnotify:\n  - webhook: {}\n",
			exp:  "notify[0].webhook.url: required",
		},
		{
			name: "incidentio without an api key",
			yaml: minimal + "\nnotify:\n  - incidentio: {}\n",
			exp:  "notify[0].incidentio.api_key: required",
		},
		{
			name: "incidents queue unnamed",
			yaml: strings.Replace(minimal, "  incidents: incidents\n", "", 1),
			exp:  "spooler.incidents: required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfig([]byte(tc.yaml))
			if err == nil {
				t.Fatal("parseConfig(): nil; want an error")
			}
			if act := err.Error(); !strings.Contains(act, tc.exp) {
				t.Errorf(
					"error: %q; want it to contain %q",
					act, tc.exp,
				)
			}
		})
	}
}
