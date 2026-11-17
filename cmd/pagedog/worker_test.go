package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	pagedog_config "github.com/spoolersh/pagedog/internal/config"
)

func TestNewWorker(t *testing.T) {
	t.Setenv("TEST_SPOOLER_API_KEY", "server-key")
	t.Setenv("TEST_WORKER_SPOOLER_API_KEY", "worker-key")
	t.Setenv("TEST_ANTHROPIC_API_KEY", "sk-ant")
	t.Setenv("TEST_GITHUB_TOKEN", "ghp")
	t.Setenv("TEST_MCP_TOKEN", "mcp-token")
	// The SDKs' own variables are this machine's: out of the way.
	for _, name := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(name, "")
	}
	taskFile := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(taskFile, []byte("Look at {{.Raw}}"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := spoolerConfig{
		Host: "127.0.0.1:8080",
		APIKey: pagedog_config.Secret{
			Env: "TEST_SPOOLER_API_KEY",
		},
		NoTLS:     true,
		Spool:     "default",
		Incidents: "incidents",
		Reports:   "reports",
	}
	own := spoolerConfig{
		Host: "host.docker.internal:8080",
		APIKey: pagedog_config.Secret{
			Env: "TEST_WORKER_SPOOLER_API_KEY",
		},
		NoTLS:     true,
		Spool:     "default",
		Incidents: "incidents",
		Reports:   "reports",
	}
	claude := modelConfig{
		Claude: &claudeConfig{
			APIKey: pagedog_config.Secret{
				Env: "TEST_ANTHROPIC_API_KEY",
			},
		},
	}
	fs := reportConfig{
		FS: &fsReportConfig{
			Dir: "reports",
		},
	}
	for _, tc := range []struct {
		name   string
		worker workerConfig
		env    map[string]string

		// check looks at the worker made, or err is a part of the error's
		// text.
		check func(t *testing.T, w *worker)
		err   string
	}{
		{
			name: "reaches the file's spooler when the worker names none",
			worker: workerConfig{
				Model:  claude,
				Report: fs,
			},
			check: func(t *testing.T, w *worker) {
				if w.spooler.Host != "127.0.0.1:8080" || w.spoolerAPIKey != "server-key" {
					t.Errorf(
						"spooler: %s under %q; want 127.0.0.1:8080 under \"server-key\"",
						w.spooler.Host, w.spoolerAPIKey,
					)
				}
			},
		},
		{
			name: "reaches the worker's own spooler",
			worker: workerConfig{
				Spooler: &own,
				Model:   claude,
				Report:  fs,
			},
			check: func(t *testing.T, w *worker) {
				if w.spooler.Host != "host.docker.internal:8080" || w.spoolerAPIKey != "worker-key" {
					t.Errorf(
						"spooler: %s under %q; want host.docker.internal:8080 under \"worker-key\"",
						w.spooler.Host, w.spoolerAPIKey,
					)
				}
			},
		},
		{
			name: "reads the model's key from where the file says",
			worker: workerConfig{
				Model:  claude,
				Report: fs,
			},
			check: func(t *testing.T, w *worker) {
				if w.modelAPIKey != "sk-ant" {
					t.Errorf(
						"model key: %q; want \"sk-ant\"",
						w.modelAPIKey,
					)
				}
			},
		},
		{
			name: "leaves the model's key to the sdk when its variable is set",
			worker: workerConfig{
				Model: modelConfig{
					Claude: &claudeConfig{},
				},
				Report: fs,
			},
			env: map[string]string{
				"ANTHROPIC_API_KEY": "sk-env",
			},
			check: func(t *testing.T, w *worker) {
				if w.modelAPIKey != "" {
					t.Errorf(
						"model key: %q; want none, the sdk's own",
						w.modelAPIKey,
					)
				}
			},
		},
		{
			name: "refuses a model with no key anywhere",
			worker: workerConfig{
				Model: modelConfig{
					Claude: &claudeConfig{},
				},
				Report: fs,
			},
			err: "worker.model.claude.api_key: not set, and the environment has no ANTHROPIC_API_KEY",
		},
		{
			name: "refuses a key that reads empty rather than fall back",
			worker: workerConfig{
				Model: modelConfig{
					Claude: &claudeConfig{
						APIKey: pagedog_config.Secret{
							Env: "TEST_EMPTY_KEY",
						},
					},
				},
				Report: fs,
			},
			env: map[string]string{
				"TEST_EMPTY_KEY":    "",
				"ANTHROPIC_API_KEY": "sk-env",
			},
			err: "worker.model.claude.api_key: empty",
		},
		{
			name: "names both of gemini's variables",
			worker: workerConfig{
				Model: modelConfig{
					Gemini: &geminiConfig{},
				},
				Report: fs,
			},
			err: "worker.model.gemini.api_key: not set, and the environment has no GEMINI_API_KEY or GOOGLE_API_KEY",
		},
		{
			name: "reads the task file",
			worker: workerConfig{
				Model: claude,
				Task: pagedog_config.Value{
					Secret: pagedog_config.Secret{
						File: taskFile,
					},
				},
				Report: fs,
			},
			check: func(t *testing.T, w *worker) {
				var sb strings.Builder
				if err := w.task.Execute(&sb, taskData{Raw: "x"}); err != nil {
					t.Fatal(err)
				}
				if act := sb.String(); act != "Look at x" {
					t.Errorf(
						"task: %q; want \"Look at x\"",
						act,
					)
				}
			},
		},
		{
			name: "refuses a task naming what the incident has not",
			worker: workerConfig{
				Model: claude,
				Task: pagedog_config.Value{
					Literal: "{{.Nope}}",
				},
				Report: fs,
			},
			err: "worker.task: ",
		},
		{
			name: "reads the mcp headers",
			worker: workerConfig{
				Model: claude,
				Tools: toolsConfig{
					MCP: []mcpToolConfig{
						{
							Name: "x",
							URL:  "http://x/mcp",
							Headers: map[string]pagedog_config.Value{
								"X-Tenant": {
									Literal: "acme",
								},
								"Authorization": {
									Secret: pagedog_config.Secret{
										Env: "TEST_MCP_TOKEN",
									},
								},
							},
						},
					},
				},
				Report: fs,
			},
			check: func(t *testing.T, w *worker) {
				exp := []map[string]string{
					{
						"X-Tenant":      "acme",
						"Authorization": "mcp-token",
					},
				}
				if !cmp.Equal(exp, w.mcpHeaders) {
					t.Errorf(
						"mcp headers: %s",
						cmp.Diff(exp, w.mcpHeaders),
					)
				}
			},
		},
		{
			name: "sends the bearer as the authorization header",
			worker: workerConfig{
				Model: claude,
				Tools: toolsConfig{
					MCP: []mcpToolConfig{
						{
							Name: "x",
							URL:  "http://x/mcp",
							Headers: map[string]pagedog_config.Value{
								"X-Tenant": {
									Literal: "acme",
								},
							},
							Auth: &mcpAuthConfig{
								Bearer: pagedog_config.Secret{
									Env: "TEST_MCP_TOKEN",
								},
							},
						},
					},
				},
				Report: fs,
			},
			check: func(t *testing.T, w *worker) {
				exp := []map[string]string{
					{
						"X-Tenant":      "acme",
						"Authorization": "Bearer mcp-token",
					},
				}
				if !cmp.Equal(exp, w.mcpHeaders) {
					t.Errorf(
						"mcp headers: %s",
						cmp.Diff(exp, w.mcpHeaders),
					)
				}
			},
		},
		{
			name: "reads the github token",
			worker: workerConfig{
				Model: claude,
				Report: reportConfig{
					GitHub: &githubReportConfig{
						Owner: "acme",
						Repo:  "incidents",
						Token: pagedog_config.Secret{
							Env: "TEST_GITHUB_TOKEN",
						},
					},
				},
			},
			check: func(t *testing.T, w *worker) {
				if w.githubToken != "ghp" {
					t.Errorf(
						"github token: %q; want \"ghp\"",
						w.githubToken,
					)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, v := range tc.env {
				t.Setenv(name, v)
			}
			cfg := config{
				Spooler: server,
				Worker:  tc.worker,
			}
			w, err := newWorker(context.Background(), &cfg)
			if tc.err != "" {
				if err == nil {
					t.Fatal("newWorker(): nil; want an error")
				}
				if act := err.Error(); !strings.Contains(act, tc.err) {
					t.Errorf(
						"error: %q; want it to contain %q",
						act, tc.err,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, w)
		})
	}
}
