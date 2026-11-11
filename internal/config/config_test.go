package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestUnmarshal(t *testing.T) {
	type conf struct {
		Name   string `yaml:"name"`
		Secret Secret `yaml:"secret"`
	}
	for _, tc := range []struct {
		name string
		yaml string
		exp  conf

		// expErr is a part of the error's text; empty means no error.
		expErr string
	}{
		{
			name: "records an environment variable",
			yaml: "name: a\nsecret: {env: TEST_SECRET}\n",
			exp: conf{
				Name: "a",
				Secret: Secret{
					Env: "TEST_SECRET",
				},
			},
		},
		{
			name: "records a file",
			yaml: "secret: {file: /run/secrets/a}\n",
			exp: conf{
				Secret: Secret{
					File: "/run/secrets/a",
				},
			},
		},
		{
			name: "records a command",
			yaml: "secret: {exec: {cmd: op, args: [read, 'op://v/i/f']}}\n",
			exp: conf{
				Secret: Secret{
					Exec: Exec{
						Command: "op",
						Args:    []string{"read", "op://v/i/f"},
					},
				},
			},
		},
		{
			name: "leaves an absent secret zero",
			yaml: "name: a\n",
			exp: conf{
				Name: "a",
			},
		},
		{
			name:   "refuses an unknown key",
			yaml:   "name: a\nnmae: b\n",
			expErr: "nmae",
		},
		{
			name:   "refuses a secret written as its value",
			yaml:   "secret: s3cret\n",
			expErr: "a secret is written as where it comes from",
		},
		{
			name:   "refuses a secret from nowhere",
			yaml:   "secret: {}\n",
			expErr: "exactly one of env, file or exec",
		},
		{
			name:   "refuses a secret from two places",
			yaml:   "secret: {env: A, file: /b}\n",
			expErr: "exactly one of env, file or exec",
		},
		{
			name:   "refuses an unknown source",
			yaml:   "secret: {vault: x}\n",
			expErr: "vault",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var act conf
			err := Unmarshal([]byte(tc.yaml), &act)
			if tc.expErr != "" {
				if err == nil {
					t.Fatal("Unmarshal(): nil; want an error")
				}
				if act := err.Error(); !strings.Contains(act, tc.expErr) {
					t.Errorf(
						"error: %q; want it to contain %q",
						act, tc.expErr,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"conf: %s",
					cmp.Diff(tc.exp, act),
				)
			}
		})
	}
}

func TestValue(t *testing.T) {
	t.Setenv("TEST_SECRET", "s3cret")

	type conf struct {
		Env map[string]Value `yaml:"env"`
	}
	for _, tc := range []struct {
		name string
		yaml string
		exp  conf

		// expUnwrapped is each value read.
		expUnwrapped map[string]string

		// expErr is a part of the error's text; empty means no error.
		expErr string
	}{
		{
			name: "records a literal as itself and a secret as its source",
			yaml: "env:\n  TZ: UTC\n  PORT: 8080\n  KEY: {env: TEST_SECRET}\n",
			exp: conf{
				Env: map[string]Value{
					"TZ":   {Literal: "UTC"},
					"PORT": {Literal: "8080"},
					"KEY":  {Secret: Secret{Env: "TEST_SECRET"}},
				},
			},
			expUnwrapped: map[string]string{
				"TZ":   "UTC",
				"PORT": "8080",
				"KEY":  "s3cret",
			},
		},
		{
			name:   "refuses a list",
			yaml:   "env:\n  TZ: [UTC]\n",
			expErr: "a value is written as itself",
		},
		{
			name:   "refuses a secret from nowhere",
			yaml:   "env:\n  KEY: {}\n",
			expErr: "exactly one of env, file or exec",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var act conf
			err := Unmarshal([]byte(tc.yaml), &act)
			if tc.expErr != "" {
				if err == nil {
					t.Fatal("Unmarshal(): nil; want an error")
				}
				if act := err.Error(); !strings.Contains(act, tc.expErr) {
					t.Errorf(
						"error: %q; want it to contain %q",
						act, tc.expErr,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"conf: %s",
					cmp.Diff(tc.exp, act),
				)
			}
			unwrapped := make(map[string]string, len(act.Env))
			for k, v := range act.Env {
				s, err := v.Unwrap(context.Background())
				if err != nil {
					t.Fatalf("Unwrap(%s): %v", k, err)
				}
				unwrapped[k] = s
			}
			if !cmp.Equal(tc.expUnwrapped, unwrapped) {
				t.Errorf(
					"unwrapped: %s",
					cmp.Diff(tc.expUnwrapped, unwrapped),
				)
			}
		})
	}
}

func TestUnwrap(t *testing.T) {
	t.Setenv("TEST_SECRET", "s3cret")

	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		secret Secret
		exp    string

		// expErr is a part of the error's text; empty means no error.
		expErr string
	}{
		{
			name: "reads an environment variable",
			secret: Secret{
				Env: "TEST_SECRET",
			},
			exp: "s3cret",
		},
		{
			name: "reads a file, trimmed",
			secret: Secret{
				File: path,
			},
			exp: "s3cret",
		},
		{
			name: "reads a command's output, trimmed",
			secret: Secret{
				Exec: Exec{
					Command: "echo",
					Args:    []string{"s3cret"},
				},
			},
			exp: "s3cret",
		},
		{
			name: "reads a zero secret as empty",
		},
		{
			name: "fails on a variable not set",
			secret: Secret{
				Env: "TEST_UNSET",
			},
			expErr: "TEST_UNSET is not set",
		},
		{
			name: "fails on a file that is not there",
			secret: Secret{
				File: path + ".missing",
			},
			expErr: "secret.missing",
		},
		{
			name: "fails on a command that fails, saying why",
			secret: Secret{
				Exec: Exec{
					Command: "sh",
					Args:    []string{"-c", "echo s3cret; echo no vault >&2; exit 3"},
				},
			},
			expErr: "exit status 3: no vault",
		},
		{
			name: "fails on a command that is not there",
			secret: Secret{
				Exec: Exec{
					Command: "pagedog-no-such-helper",
				},
			},
			expErr: "pagedog-no-such-helper",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act, err := tc.secret.Unwrap(context.Background())
			if tc.expErr != "" {
				if err == nil {
					t.Fatal("Unwrap(): nil; want an error")
				}
				if act := err.Error(); !strings.Contains(act, tc.expErr) {
					t.Errorf(
						"error: %q; want it to contain %q",
						act, tc.expErr,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if act != tc.exp {
				t.Errorf(
					"secret: %q; want %q",
					act, tc.exp,
				)
			}
		})
	}
}
