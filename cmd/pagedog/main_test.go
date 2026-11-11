package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"
	pagedog_config "github.com/spoolersh/pagedog/internal/config"
)

func TestWorkerEnv(t *testing.T) {
	t.Setenv("TEST_SPOOLER_API_KEY", "local")
	t.Setenv("TEST_ANTHROPIC_API_KEY", "sk-ant")

	act := workerEnv(context.Background(), "workers.runtime.exec.env", map[string]pagedog_config.Value{
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
	})
	exp := map[string]string{
		"TZ":                      "UTC",
		"PAGEDOG_SPOOLER_API_KEY": "local",
		"ANTHROPIC_API_KEY":       "sk-ant",
	}
	if !cmp.Equal(exp, act) {
		t.Errorf(
			"env: %s",
			cmp.Diff(exp, act),
		)
	}
}

func TestWorkerArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config
		exp  []string
	}{
		{
			name: "passes the spooler settings alone when the worker has defaults",
			cfg: config{
				Spooler: spoolerConfig{
					Host:  "memspoolerd:8080",
					NoTLS: true,
					Spool: "default",
					Incidents: queueConfig{
						Name: "incidents",
					},
					Reports: queueConfig{
						Name: "reports",
					},
				},
				Worker: workerConfig{
					Model: modelConfig{
						Claude: &claudeConfig{},
					},
				},
			},
			exp: []string{
				"--log-level", "info",
				"--spooler.host", "memspoolerd:8080",
				"--spooler.no-tls=true",
				"--spooler.spool", "default",
				"--spooler.input", "incidents",
				"--spooler.output", "reports",
				"--agent.model", "claude",
			},
		},
		{
			name: "passes what the file sets",
			cfg: config{
				Spooler: spoolerConfig{
					Host:  "memspoolerd:8080",
					Spool: "default",
					Incidents: queueConfig{
						Name: "incidents",
					},
					Reports: queueConfig{
						Name: "reports",
					},
				},
				Worker: workerConfig{
					Model: modelConfig{
						Claude: &claudeConfig{
							Name:      "claude-opus-5-5",
							MaxTokens: 2048,
						},
					},
					MaxTurns: 5,
					Task:     "Look at {{.Raw}}",
					Tools: toolsConfig{
						Prometheus: &prometheusToolConfig{
							URL: "http://prometheus:9090",
						},
						Loki: &lokiToolConfig{
							URL: "http://loki:3100",
						},
					},
					Report: reportConfig{
						FS: &fsReportConfig{
							Dir: "/reports",
						},
					},
				},
			},
			exp: []string{
				"--log-level", "info",
				"--spooler.host", "memspoolerd:8080",
				"--spooler.no-tls=false",
				"--spooler.spool", "default",
				"--spooler.input", "incidents",
				"--spooler.output", "reports",
				"--agent.model", "claude",
				"--agent.claude.name", "claude-opus-5-5",
				"--agent.claude.max-tokens", "2048",
				"--agent.max-turns", "5",
				"--agent.task", "Look at {{.Raw}}",
				"--agent.tools", "prometheus,loki",
				"--agent.prometheus.url", "http://prometheus:9090",
				"--agent.loki.url", "http://loki:3100",
				"--report.store", "fs",
				"--report.fs.dir", "/reports",
			},
		},
		{
			name: "passes each mcp server as json",
			cfg: config{
				Spooler: spoolerConfig{
					Host:  "memspoolerd:8080",
					Spool: "default",
					Incidents: queueConfig{
						Name: "incidents",
					},
					Reports: queueConfig{
						Name: "reports",
					},
				},
				Worker: workerConfig{
					Model: modelConfig{
						Claude: &claudeConfig{},
					},
					Tools: toolsConfig{
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
				},
			},
			exp: []string{
				"--log-level", "info",
				"--spooler.host", "memspoolerd:8080",
				"--spooler.no-tls=false",
				"--spooler.spool", "default",
				"--spooler.input", "incidents",
				"--spooler.output", "reports",
				"--agent.model", "claude",
				"--agent.tools", "mcp",
				"--agent.mcp.server", `{"name":"prometheus","url":"http://prometheus-mcp:8080/mcp","headers":{"Authorization":{"env":"PROMETHEUS_MCP_TOKEN"},"X-Scope-OrgID":{"value":"tenant-1"}},"tools":["query","range_query"]}`,
				"--agent.mcp.server", `{"name":"inventory","command":"/usr/lib/pagedog/inventory-tool","args":["--verbose"],"env":["HOME"]}`,
			},
		},
		{
			name: "names another model's settings after it",
			cfg: config{
				Spooler: spoolerConfig{
					Host:  "memspoolerd:8080",
					Spool: "default",
					Incidents: queueConfig{
						Name: "incidents",
					},
					Reports: queueConfig{
						Name: "reports",
					},
				},
				Worker: workerConfig{
					Model: modelConfig{
						Gemini: &geminiConfig{
							Name: "gemini-4-carbon",
						},
					},
				},
			},
			exp: []string{
				"--log-level", "info",
				"--spooler.host", "memspoolerd:8080",
				"--spooler.no-tls=false",
				"--spooler.spool", "default",
				"--spooler.input", "incidents",
				"--spooler.output", "reports",
				"--agent.model", "gemini",
				"--agent.gemini.name", "gemini-4-carbon",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act := workerArgs(&tc.cfg, slog.LevelInfo)
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"args: %s",
					cmp.Diff(tc.exp, act),
				)
			}
		})
	}
}
