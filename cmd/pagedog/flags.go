package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"strings"
)

// MCPServer is an MCP server as the launcher tells a worker about it: one
// --agent.mcp.server flag, holding the server as JSON.
type MCPServer struct {
	// Name names the server in errors and logs.
	Name string `json:"name"`

	// URL is the server's endpoint, for one reached over HTTP; Headers are
	// set on every request to it.
	URL     string               `json:"url,omitempty"`
	Headers map[string]MCPHeader `json:"headers,omitempty"`

	// Command is the server's program, for one run as a child process; Args
	// are its arguments, and Env names the variables of the worker's
	// environment it gets, which is all it gets.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`

	// Tools names the tools offered to the model.
	//
	// Zero means all the server offers.
	Tools []string `json:"tools,omitempty"`
}

// MCPHeader is one header of an [MCPServer] reached over HTTP: Value as
// written or, for a secret, Env naming the variable of the worker's
// environment that holds it. Nothing secret is on a command line.
type MCPHeader struct {
	Value string `json:"value,omitempty"`
	Env   string `json:"env,omitempty"`
}

// MCPServersVar returns a [flag.Value] that appends to ss one [MCPServer]
// per flag, from its JSON.
func MCPServersVar(ss *[]MCPServer) flag.Value {
	return (*mcpServersFlag)(ss)
}

type mcpServersFlag []MCPServer

func (f *mcpServersFlag) String() string {
	ss := make([]string, len(*f))
	for i, s := range *f {
		data, _ := json.Marshal(s)
		ss[i] = string(data)
	}
	return strings.Join(ss, " ")
}

func (f *mcpServersFlag) Set(v string) error {
	dec := json.NewDecoder(strings.NewReader(v))
	dec.DisallowUnknownFields()
	var s MCPServer
	if err := dec.Decode(&s); err != nil {
		return err
	}
	*f = append(*f, s)
	return nil
}

// StringsVar returns a [flag.Value] that sets ss from a comma-separated
// list, blanks dropped.
func StringsVar(ss *[]string) flag.Value {
	return (*stringsFlag)(ss)
}

type stringsFlag []string

func (s *stringsFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringsFlag) Set(v string) error {
	*s = nil
	for p := range strings.SplitSeq(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

// LogLevelVar returns a [flag.Value] that sets level from its name: debug,
// info, warn, error or off.
func LogLevelVar(level *slog.Level) flag.Value {
	return (*levelFlag)(level)
}

type levelFlag slog.Level

const levelOff = levelFlag(slog.LevelError + 8)

func (l *levelFlag) String() string {
	if *l >= levelOff {
		return "off"
	}
	return strings.ToLower(slog.Level(*l).String())
}

func (l *levelFlag) Set(s string) error {
	switch strings.ToLower(s) {
	case "debug":
		*l = levelFlag(slog.LevelDebug)
	case "info":
		*l = levelFlag(slog.LevelInfo)
	case "warn":
		*l = levelFlag(slog.LevelWarn)
	case "error":
		*l = levelFlag(slog.LevelError)
	case "off":
		*l = levelOff
	default:
		return fmt.Errorf(
			"unexpected log level: %q",
			s,
		)
	}
	return nil
}
