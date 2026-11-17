package main

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	pagedog_config "github.com/spoolersh/pagedog/internal/config"
)

func TestServerBanner(t *testing.T) {
	cfg := config{
		Spooler: spoolerConfig{
			Host: "api.spooler.sh",
			APIKey: pagedog_config.Secret{
				Env: "SPOOLER_API_KEY",
			},
			Spool:     "default",
			Incidents: "incidents",
			Reports:   "reports",
		},
		Webhook: webhookConfig{
			Listen: ":7070",
			Sources: []sourceConfig{
				{
					Alertmanager: &alertmanagerConfig{
						Token: pagedog_config.Secret{
							File: "/run/secrets/alertmanager-token",
						},
					},
				},
			},
		},
		Workers: workersConfig{
			Max: 5,
			Runtime: runtimeConfig{
				Exec: &execConfig{
					Path: "/usr/local/bin/pagedog",
					Args: []string{"worker", "--config", "pagedog.yaml"},
					Env: map[string]pagedog_config.Value{
						"TZ": {
							Literal: "UTC",
						},
						"SPOOLER_API_KEY": {
							Secret: pagedog_config.Secret{
								Env: "SPOOLER_API_KEY",
							},
						},
					},
				},
			},
		},
		Worker: workerConfig{
			Spooler: &spoolerConfig{
				Host: "host.docker.internal:8080",
				APIKey: pagedog_config.Secret{
					Env: "SPOOLER_API_KEY",
				},
				NoTLS:     true,
				Spool:     "default",
				Incidents: "incidents",
				Reports:   "reports",
			},
			Model: modelConfig{
				Claude: &claudeConfig{
					Name: "claude-opus-5-5",
					APIKey: pagedog_config.Secret{
						File: "/run/secrets/anthropic",
					},
				},
			},
			Task: pagedog_config.Value{
				Secret: pagedog_config.Secret{
					File: "/etc/pagedog/task.md",
				},
			},
			Tools: toolsConfig{
				Prometheus: &prometheusToolConfig{
					URL: "http://prometheus:9090",
				},
				MCP: []mcpToolConfig{
					{
						Name: "prometheus",
						URL:  "http://prometheus-mcp:8080/mcp",
						Headers: map[string]pagedog_config.Value{
							"X-Scope-OrgID": {
								Literal: "tenant-1",
							},
						},
						Auth: &mcpAuthConfig{
							Bearer: pagedog_config.Secret{
								Env: "PROMETHEUS_MCP_TOKEN",
							},
						},
						Tools: []string{"query"},
					},
				},
			},
			Report: reportConfig{
				GitHub: &githubReportConfig{
					Owner: "acme",
					Repo:  "incidents",
					Token: pagedog_config.Secret{
						Exec: pagedog_config.Exec{
							Command: "op",
							Args:    []string{"read", "op://vault/github/token"},
						},
					},
				},
			},
		},
		Notify: []notifierConfig{
			{
				Slack: &slackNotifierConfig{
					URL: pagedog_config.Secret{
						Env: "SLACK_WEBHOOK_URL",
					},
				},
			},
			{
				Log: &logNotifierConfig{},
			},
		},
	}
	var out bytes.Buffer
	if err := printBanner(&out, serverBannerTemplate, makeServerBanner(&cfg, "pagedog.yaml", slog.LevelDebug)); err != nil {
		t.Fatal(err)
	}
	act := out.String()
	for _, tc := range []struct {
		name string
		exp  string
	}{
		{
			name: "names the file and the log level",
			exp:  `config: +pagedog.yaml\nlog level: +debug`,
		},
		{
			name: "says where the spooler key comes from",
			exp:  `api key: +from env SPOOLER_API_KEY`,
		},
		{
			name: "says where a source's secret comes from",
			exp:  `source: +alertmanager, token from file /run/secrets/alertmanager-token`,
		},
		{
			name: "describes the runtime and its environment",
			exp:  `workers: +exec /usr/local/bin/pagedog worker --config pagedog.yaml, max 5\nenv: +SPOOLER_API_KEY from env SPOOLER_API_KEY\nenv: +TZ UTC`,
		},
		{
			name: "describes the spooler the workers reach when it is their own",
			exp:  `worker spooler: +host.docker.internal:8080 \(no tls\), spool default, api key from env SPOOLER_API_KEY, queues incidents -> reports`,
		},
		{
			name: "fills the model's defaults in and says where its key comes from",
			exp:  `model: +claude claude-opus-5-5, max tokens 4096\nmodel key: +from file /run/secrets/anthropic\nturns: +10\ntask: +from file /etc/pagedog/task.md`,
		},
		{
			name: "lists the tools, the bearer by its source",
			exp:  `tool: +prometheus http://prometheus:9090\ntool: +mcp prometheus http://prometheus-mcp:8080/mcp, header X-Scope-OrgID tenant-1, bearer from env PROMETHEUS_MCP_TOKEN, tools query`,
		},
		{
			name: "names the store and the command its token comes from",
			exp:  `report: +github acme/incidents, token from exec op\n`,
		},
		{
			name: "lists the notifiers",
			exp:  `notify: +slack, url from env SLACK_WEBHOOK_URL\nnotify: +log`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !regexp.MustCompile(tc.exp).MatchString(act) {
				t.Errorf(
					"banner:\n%s\nwant it to match %q",
					act, tc.exp,
				)
			}
		})
	}
	// The command's arguments never show: they may name where a secret is.
	if strings.Contains(act, "op://") {
		t.Errorf(
			"banner:\n%s\nwant the exec's arguments left out",
			act,
		)
	}
}

func TestServerBannerDocker(t *testing.T) {
	cfg := config{
		Workers: workersConfig{
			Runtime: runtimeConfig{
				Docker: &dockerConfig{
					Image:   "spoolersh/pagedog",
					Command: []string{"worker", "--config", "/etc/pagedog/worker.yaml"},
					Files: map[string]string{
						"/etc/pagedog/worker.yaml": "worker.yaml",
					},
					Network: "pagedog_default",
				},
			},
		},
		Worker: workerConfig{
			Model: modelConfig{
				Claude: &claudeConfig{},
			},
		},
	}
	var out bytes.Buffer
	if err := printBanner(&out, serverBannerTemplate, makeServerBanner(&cfg, "pagedog.yaml", slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	act := out.String()
	exp := `workers: +docker spoolersh/pagedog worker --config /etc/pagedog/worker.yaml, network pagedog_default, max 3\nfile: +/etc/pagedog/worker.yaml from worker.yaml\n`
	if !regexp.MustCompile(exp).MatchString(act) {
		t.Errorf(
			"banner:\n%s\nwant it to match %q",
			act, exp,
		)
	}
	if strings.Contains(act, "worker spooler") {
		t.Errorf(
			"banner:\n%s\nwant no worker spooler line: the workers reach this process's",
			act,
		)
	}
}

func TestWorkerBanner(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-s3cr3t")
	cfg := config{
		Spooler: spoolerConfig{
			Host: "memspoolerd:8080",
			APIKey: pagedog_config.Secret{
				Env: "SPOOLER_API_KEY",
			},
			NoTLS:     true,
			Spool:     "default",
			Incidents: "incidents",
			Reports:   "reports",
		},
		Worker: workerConfig{
			Model: modelConfig{
				Claude: &claudeConfig{},
			},
			Tools: toolsConfig{
				Loki: &lokiToolConfig{
					URL: "http://loki:3100",
				},
				MCP: []mcpToolConfig{
					{
						Name:    "inventory",
						Command: "/usr/lib/pagedog/inventory-tool",
						Args:    []string{"--verbose"},
						Env:     []string{"HOME"},
					},
				},
			},
			Report: reportConfig{
				GitHub: &githubReportConfig{
					Owner: "acme",
					Repo:  "incidents",
					Token: pagedog_config.Secret{
						Env: "GITHUB_TOKEN",
					},
				},
			},
		},
	}
	var out bytes.Buffer
	if err := printBanner(&out, workerBannerTemplate, makeWorkerBanner(&cfg, "/etc/pagedog/pagedog.yaml", slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	act := out.String()
	for _, tc := range []struct {
		name string
		exp  string
	}{
		{
			name: "names the file",
			exp:  `config: +/etc/pagedog/pagedog.yaml\n`,
		},
		{
			name: "reaches the file's spooler, the key by its source",
			exp:  `spooler: +memspoolerd:8080 \(no tls\), spool default\napi key: +from env SPOOLER_API_KEY\nqueues: +incidents -> reports\n`,
		},
		{
			name: "says the sdk's own variable is set, not what it is",
			exp:  `model: +claude claude-opus-5-5, max tokens 4096\nmodel key: +env ANTHROPIC_API_KEY, set`,
		},
		{
			name: "lists the tools, a program with its arguments and variables",
			exp:  `tool: +loki http://loki:3100\ntool: +mcp inventory /usr/lib/pagedog/inventory-tool --verbose, env HOME`,
		},
		{
			name: "says where the github token comes from",
			exp:  `report: +github acme/incidents, token from env GITHUB_TOKEN`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !regexp.MustCompile(tc.exp).MatchString(act) {
				t.Errorf(
					"banner:\n%s\nwant it to match %q",
					act, tc.exp,
				)
			}
		})
	}
	if strings.Contains(act, "sk-ant-s3cr3t") {
		t.Errorf(
			"banner:\n%s\nwant the key's value left out",
			act,
		)
	}
}
