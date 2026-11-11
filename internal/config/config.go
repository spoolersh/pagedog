// Package config reads YAML configuration files: strictly, and with the
// secrets left where the file says they are until they are needed.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Unmarshal reads a configuration file's contents into v. A key v has no
// field for is an error.
func Unmarshal(data []byte, v any) error {
	return yaml.Load(data, v, yaml.WithKnownFields(true))
}

// Secret is where a secret comes from, as the file says it: exactly one of
//
//	{env: NAME}
//	{file: PATH}
//	{exec: {cmd: COMMAND, args: [ARGS...]}}
//
// Reading the file only records the source; [Secret.Unwrap] reads the
// value.
//
// Zero means no secret.
type Secret struct {
	// Env names an environment variable.
	Env string `yaml:"env"`

	// File is a file's path.
	File string `yaml:"file"`

	// Exec is a command to run; what it prints is the secret.
	Exec Exec `yaml:"exec"`
}

// Exec is a command to run for a [Secret].
type Exec struct {
	// Command is the program: a path, or a name looked up in PATH.
	Command string `yaml:"cmd"`

	// Dir is the working directory.
	//
	// Zero means this process's.
	Dir string `yaml:"dir"`

	// Args are the arguments, the program name excluded.
	Args []string `yaml:"args"`
}

// IsZero reports whether no source is set.
func (s Secret) IsZero() bool {
	return s.Env == "" && s.File == "" && s.Exec.Command == ""
}

// UnmarshalYAML records the source, checking that it is exactly one.
func (s *Secret) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return errors.New(
			"a secret is written as where it comes from: " +
				"{env: NAME}, {file: PATH} or {exec: {cmd: COMMAND, args: [ARGS...]}}",
		)
	}
	// plain has no UnmarshalYAML, so the node decodes into the fields.
	type plain Secret
	var src plain
	if err := n.Load(&src, yaml.WithKnownFields(true)); err != nil {
		return err
	}
	var set int
	for _, has := range []bool{
		src.Env != "",
		src.File != "",
		src.Exec.Command != "",
	} {
		if has {
			set++
		}
	}
	if set != 1 {
		return errors.New(
			"written as exactly one of env, file or exec",
		)
	}
	*s = Secret(src)
	return nil
}

// Unwrap reads the secret: the environment variable, the file's contents,
// or what the command prints to its standard output, trailing line breaks
// trimmed. A command that fails has what it printed to its standard error
// in the error, and its output stays out of it.
func (s Secret) Unwrap(ctx context.Context) (string, error) {
	var v string
	switch {
	case s.Env != "":
		var has bool
		v, has = os.LookupEnv(s.Env)
		if !has {
			return "", fmt.Errorf(
				"environment variable %s is not set",
				s.Env,
			)
		}

	case s.File != "":
		data, err := os.ReadFile(s.File)
		if err != nil {
			return "", err
		}
		v = string(data)

	case s.Exec.Command != "":
		cmd := exec.CommandContext(ctx, s.Exec.Command, s.Exec.Args...)
		cmd.Dir = s.Exec.Dir
		out, err := cmd.Output()
		if err != nil {
			// Output() keeps the standard error of a command that exits
			// non-zero, which is where it says why.
			var exit *exec.ExitError
			if errors.As(err, &exit) && len(exit.Stderr) > 0 {
				return "", fmt.Errorf(
					"%s: %w: %s",
					cmd, err, bytes.TrimSpace(exit.Stderr),
				)
			}
			return "", fmt.Errorf(
				"%s: %w",
				cmd, err,
			)
		}
		v = string(out)

	default:
		return "", nil
	}
	return strings.TrimRight(v, "\r\n"), nil
}

// Value is a string as the file writes it: as itself, or as where it comes
// from, the way [Secret] is written. Reading the file records which;
// [Value.Unwrap] gives the string.
//
// Zero is the empty string.
type Value struct {
	// Literal is the string, written as itself.
	Literal string

	// Secret is where the string comes from, written as a secret.
	Secret Secret
}

// IsSecret reports whether the value is written as a secret.
func (v Value) IsSecret() bool {
	return !v.Secret.IsZero()
}

// UnmarshalYAML records a scalar as the literal and a mapping as the secret.
func (v *Value) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Load(&v.Literal)
	case yaml.MappingNode:
		return n.Load(&v.Secret)
	default:
		return errors.New(
			"a value is written as itself, or as where it comes from: " +
				"{env: NAME}, {file: PATH} or {exec: {cmd: COMMAND, args: [ARGS...]}}",
		)
	}
}

// Unwrap gives the string: the literal, or the secret read.
func (v Value) Unwrap(ctx context.Context) (string, error) {
	if v.IsSecret() {
		return v.Secret.Unwrap(ctx)
	}
	return v.Literal, nil
}
