package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spoolersh/pagedog/internal/ai"
)

type echoInput struct {
	Text string `json:"text" jsonschema:"what to echo"`
}

// connect runs a server with two tools in memory, echo and fail, and
// returns a session on it.
func connect(t *testing.T) *mcp.ClientSession {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test",
		Version: "0",
	}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes the text.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: in.Text,
				},
			},
		}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "fail",
		Description: "Fails.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: "no such thing: " + in.Text,
				},
			},
		}, nil, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0",
	}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		session.Close()
	})
	return session
}

func TestTools(t *testing.T) {
	session := connect(t)
	for _, tc := range []struct {
		name  string
		names []string

		// exp is the names of the tools returned.
		exp []string

		// expErr is a part of the error's text; empty means no error.
		expErr string
	}{
		{
			name: "keeps every tool when none is named",
			exp:  []string{"echo", "fail"},
		},
		{
			name:  "keeps the tools named",
			names: []string{"fail"},
			exp:   []string{"fail"},
		},
		{
			name:   "refuses a name the server does not offer",
			names:  []string{"echo", "nope"},
			expErr: `no tool "nope"; the server offers echo, fail`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, err := Tools(context.Background(), session, tc.names)
			if tc.expErr != "" {
				if err == nil {
					t.Fatal("Tools(): nil; want an error")
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
			var act []string
			for _, tool := range ts {
				act = append(act, tool.Spec().Name)
			}
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"tools: %s",
					cmp.Diff(tc.exp, act),
				)
			}
		})
	}
}

func TestToolSpec(t *testing.T) {
	ts, err := Tools(context.Background(), connect(t), []string{"echo"})
	if err != nil {
		t.Fatal(err)
	}
	spec := ts[0].Spec()
	if act, exp := spec.Desc, "Echoes the text."; act != exp {
		t.Errorf(
			"description: %q; want %q",
			act, exp,
		)
	}
	// The schema goes to the model as the server sent it.
	schema, err := json.Marshal(spec.Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, exp := range []string{`"type":"object"`, `"text"`, `"what to echo"`} {
		if !strings.Contains(string(schema), exp) {
			t.Errorf(
				"schema: %s; want it to contain %s",
				schema, exp,
			)
		}
	}
}

func TestToolCall(t *testing.T) {
	ts, err := Tools(context.Background(), connect(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]ai.Tool, len(ts))
	for _, tool := range ts {
		tools[tool.Spec().Name] = tool
	}
	for _, tc := range []struct {
		tool  string
		input string
		exp   string

		// expErr is a part of the error's text; empty means no error.
		expErr string
	}{
		{
			tool:  "echo",
			input: `{"text":"hi"}`,
			exp:   "hi",
		},
		{
			tool:   "fail",
			input:  `{"text":"x"}`,
			expErr: "no such thing: x",
		},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			act, err := tools[tc.tool].Call(context.Background(), []byte(tc.input))
			if tc.expErr != "" {
				if err == nil {
					t.Fatal("Call(): nil; want an error")
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
					"output: %q; want %q",
					act, tc.exp,
				)
			}
		})
	}
}
