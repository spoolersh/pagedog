// Package mcp gives an agent the tools of an MCP server.
package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spoolersh/pagedog/internal/ai"
	"github.com/spoolersh/pagedog/internal/ai/tools"
)

var _ ai.Tool = (*Tool)(nil)

// Tools lists the tools a connected server offers and returns one [Tool] for
// each kept: all of them, or those named. A name the server does not offer
// is an error, so a list gone stale is noticed at startup.
func Tools(ctx context.Context, session *mcp.ClientSession, names []string) ([]ai.Tool, error) {
	var (
		offered []string
		byName  = make(map[string]*mcp.Tool)
	)
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		offered = append(offered, t.Name)
		byName[t.Name] = t
	}
	if len(names) == 0 {
		names = offered
	}
	ts := make([]ai.Tool, 0, len(names))
	for _, name := range names {
		t, has := byName[name]
		if !has {
			return nil, fmt.Errorf(
				"no tool %q; the server offers %s",
				name, strings.Join(offered, ", "),
			)
		}
		ts = append(ts, &Tool{
			Session: session,
			Tool:    t,
		})
	}
	return ts, nil
}

// Tool is one tool of an MCP server, called through the session that listed
// it.
type Tool struct {
	Session *mcp.ClientSession
	Tool    *mcp.Tool
}

// Spec describes the tool as the server does.
func (t *Tool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name:  t.Tool.Name,
		Desc:  t.Tool.Description,
		Input: t.Tool.InputSchema,
	}
}

// Call asks the server to run the tool on the model's input. The text the
// server returns goes back; a result the server marks as an error is one.
func (t *Tool) Call(ctx context.Context, input []byte) (string, error) {
	params := &mcp.CallToolParams{
		Name: t.Tool.Name,
	}
	if len(input) > 0 {
		params.Arguments = json.RawMessage(input)
	}
	res, err := t.Session.CallTool(ctx, params)
	if err != nil {
		return "", err
	}
	out := text(res.Content)
	if res.IsError {
		return "", errors.New(cmp.Or(out, "the tool failed"))
	}
	return tools.Truncate(out), nil
}

// text joins the text of a result, one piece per line; a piece that is not
// text is said to be missing.
func text(content []mcp.Content) string {
	var sb strings.Builder
	for i, c := range content {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if t, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(t.Text)
		} else {
			sb.WriteString("[content that is not text, left out]")
		}
	}
	return sb.String()
}
