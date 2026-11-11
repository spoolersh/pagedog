package main

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	pagedog_config "github.com/spoolersh/pagedog/internal/config"
)

func TestWorkerEnv(t *testing.T) {
	t.Setenv("TEST_SPOOLER_API_KEY", "local")
	t.Setenv("TEST_ANTHROPIC_API_KEY", "sk-ant")

	act, err := workerEnv(context.Background(), "workers.runtime.exec.env", map[string]pagedog_config.Value{
		"TZ": {
			Literal: "UTC",
		},
		"SPOOLER_API_KEY": {
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
	if err != nil {
		t.Fatal(err)
	}
	exp := map[string]string{
		"TZ":                "UTC",
		"SPOOLER_API_KEY":   "local",
		"ANTHROPIC_API_KEY": "sk-ant",
	}
	if !cmp.Equal(exp, act) {
		t.Errorf(
			"env: %s",
			cmp.Diff(exp, act),
		)
	}
}
