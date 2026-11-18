// Package mcp gives an agent the tools of an MCP server.
package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spoolersh/pagedog/internal/ai"
	"github.com/spoolersh/pagedog/internal/httputil"
)

var _ ai.Tool = (*Tool)(nil)

// Client reaches one MCP server: one serving at URL, with Header set on
// every request, or one run as Command with Args, in an environment of Env
// alone, its standard error to Stderr. Exactly one of URL and Command is
// set.
type Client struct {
	// Name is what the server knows this client as.
	Name string

	URL    string
	Header http.Header

	Command string
	Args    []string

	// Env is the environment the command gets, name=value pairs, and all
	// of it: empty is an empty environment.
	Env []string

	// Stderr is where the command's standard error goes.
	//
	// Zero means it is discarded.
	Stderr io.Writer

	mu      sync.Mutex
	session *mcp.ClientSession
}

// Tools lists the tools the server offers and returns one [Tool] for each
// kept: all of them, or those named. A name the server does not offer is an
// error, so a list gone stale is noticed at startup. The first call
// connects, and the tools call through that connection until [Client.Close].
func (c *Client) Tools(ctx context.Context, names []string) ([]ai.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	return tools(ctx, session, names)
}

// Close ends the connection, if there is one; a command server is stopped
// with it.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil
	}
	err := c.session.Close()
	c.session = nil
	return err
}

// connect is the connection, made on the first call.
func (c *Client) connect(ctx context.Context) (*mcp.ClientSession, error) {
	if c.session != nil {
		return c.session, nil
	}
	var transport mcp.Transport
	switch {
	case c.URL != "":
		transport = &mcp.StreamableClientTransport{
			Endpoint: c.URL,
			HTTPClient: &http.Client{
				Transport: &httputil.HeaderTransport{
					Header: c.Header,
				},
			},
		}
	case c.Command != "":
		cmd := exec.Command(c.Command, c.Args...)
		// Nil would be this process's environment.
		cmd.Env = c.Env
		if cmd.Env == nil {
			cmd.Env = []string{}
		}
		cmd.Stderr = c.Stderr
		transport = &mcp.CommandTransport{
			Command: cmd,
		}
	default:
		return nil, errors.New("no url and no command")
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    c.Name,
		Version: "0",
	}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	c.session = session
	return session, nil
}

// tools lists the tools a connected server offers and returns one [Tool]
// for each kept: all of them, or those named.
func tools(ctx context.Context, session *mcp.ClientSession, names []string) ([]ai.Tool, error) {
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
			session: session,
			tool:    t,
		})
	}
	return ts, nil
}

// Tool is one tool of an MCP server, called through the connection that
// listed it.
type Tool struct {
	session *mcp.ClientSession
	tool    *mcp.Tool
}

// Spec describes the tool as the server does.
func (t *Tool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name:  t.tool.Name,
		Desc:  t.tool.Description,
		Input: t.tool.InputSchema,
	}
}

// Call asks the server to run the tool on the model's input. The text the
// server returns goes back; a result the server marks as an error is one.
func (t *Tool) Call(ctx context.Context, input []byte) (string, error) {
	params := &mcp.CallToolParams{
		Name: t.tool.Name,
	}
	if len(input) > 0 {
		params.Arguments = json.RawMessage(input)
	}
	res, err := t.session.CallTool(ctx, params)
	if err != nil {
		return "", err
	}
	out := text(res.Content)
	if res.IsError {
		return "", errors.New(cmp.Or(out, "the tool failed"))
	}
	return ai.TruncateToolOutput(out), nil
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
