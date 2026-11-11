// Package ai runs an LLM agent: a model, the tools it may call, and the
// conversation between them.
package ai

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// DefaultMaxTurns is how many times [Agent.Do] asks the model when the request
// does not say.
const DefaultMaxTurns = 10

// ErrTurnsExceeded is the error [Agent.Do] returns when the model has not
// answered within the turns allowed.
var ErrTurnsExceeded = errors.New("turns exceeded")

// Model is what an [Agent] talks to, one completion at a time.
type Model interface {
	Complete(context.Context, CompleteRequest) (CompleteResult, error)
}

// CompleteRequest asks a [Model] for the next assistant message.
type CompleteRequest struct {
	Messages []Message
	Tools    []ToolSpec
}

// CompleteResult is a [Model]'s reply.
type CompleteResult struct {
	Message Message
	Usage   Usage
}

// Usage counts the tokens a completion took.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

func (u Usage) add(v Usage) Usage {
	u.InputTokens += v.InputTokens
	u.OutputTokens += v.OutputTokens
	return u
}

// MessageKind is who a [Message] is from. A [Model] maps it to its own roles.
type MessageKind uint

const (
	_ MessageKind = iota // The zero value is no kind.

	// MessageKindUser is the caller: the task.
	MessageKindUser

	// MessageKindModel is the model.
	MessageKindModel

	// MessageKindTool is the tools: the results of the calls the model made in
	// the message before.
	MessageKindTool
)

// Message is one message of the conversation.
type Message struct {
	Kind   MessageKind
	Blocks []MessageBlock
}

// MessageBlock is one piece of a [Message]: a [TextBlock], a [ToolCallRequest] or a
// [ToolCallResult].
type MessageBlock interface {
	_isMessageBlock()
}

// TextBlock is a text inside a [Message].
type TextBlock struct {
	Text string
}

// ToolCallRequest is the model calling a tool.
type ToolCallRequest struct {
	// ID pairs the call with its result.
	ID string

	Name  string
	Input json.RawMessage
}

// ToolCallResult is what a [ToolCallRequest] came to.
type ToolCallResult struct {
	ID      string
	Content string
	Failure bool
}

func (TextBlock) _isMessageBlock()       {}
func (ToolCallRequest) _isMessageBlock() {}
func (ToolCallResult) _isMessageBlock()  {}

// Tool is something the model may call.
type Tool interface {
	// Spec describes the tool to the model.
	Spec() ToolSpec

	// Call runs the tool on the model's input, which the model shaped after
	// Spec's schema. The text goes back to the model; so does an error.
	Call(ctx context.Context, input []byte) (string, error)
}

type ToolFunc struct {
	DoSpec func() ToolSpec
	DoCall func(context.Context, []byte) (string, error)
}

func (f *ToolFunc) Spec() ToolSpec {
	return f.DoSpec()
}
func (f *ToolFunc) Call(ctx context.Context, p []byte) (string, error) {
	return f.DoCall(ctx, p)
}

// ToolSpec is a [Tool] as the model is told about it.
type ToolSpec struct {
	Name string
	Desc string

	// Input is the input's JSON Schema, an object: a
	// [github.com/spoolersh/pagedog/internal/jsonschema.Schema], or whatever
	// marshals to one, such as the schema a tool described elsewhere came
	// with.
	Input any
}

// MaxToolOutput bounds what a tool returns to the model, in bytes.
const MaxToolOutput = 16 << 10

// TruncateToolOutput cuts a tool's output at [MaxToolOutput], saying so.
func TruncateToolOutput(s string) string {
	if len(s) <= MaxToolOutput {
		return s
	}
	return s[:MaxToolOutput] + "\n... truncated"
}

// Agent runs a [Model] in a loop with the tools registered, until the model
// answers in text alone.
type Agent struct {
	Trace AgentTrace
	Model Model

	mu    sync.Mutex
	tools map[string]Tool
}

// RegisterTool makes t available to the model under its spec's name.
func (a *Agent) RegisterTool(t Tool) error {
	name := t.Spec().Name
	if name == "" {
		return errors.New("tool has no name")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tools == nil {
		a.tools = make(map[string]Tool)
	}
	if _, has := a.tools[name]; has {
		return fmt.Errorf(
			"tool already registered: %q",
			name,
		)
	}
	a.tools[name] = t
	return nil
}

// toolSpecs lists the tools by name, so the model sees one list every run.
func (a *Agent) toolSpecs() []ToolSpec {
	a.mu.Lock()
	defer a.mu.Unlock()
	specs := make([]ToolSpec, 0, len(a.tools))
	for _, name := range slices.Sorted(maps.Keys(a.tools)) {
		specs = append(specs, a.tools[name].Spec())
	}
	return specs
}

func (a *Agent) tool(name string) (Tool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, has := a.tools[name]
	return t, has
}

// DoRequest is a task for [Agent.Do].
type DoRequest struct {
	// Task is the user message the conversation starts with.
	Task string

	// MaxTurns is how many times the model is asked before giving up.
	//
	// Zero means [DefaultMaxTurns].
	MaxTurns int
}

// DoResult is what a task came to.
type DoResult struct {
	// Output is the model's final answer.
	Output string

	// Turns is how many times the model was asked.
	Turns int

	// Usage sums the usage of every turn.
	Usage Usage
}

// Do runs the task: it asks the model, runs the tools the model calls and
// hands the results back, until the model answers with no tool call, or the
// turns run out with [ErrTurnsExceeded]. A tool's error goes back to the model
// as the tool's result, not out of here: the model may recover or give up.
func (a *Agent) Do(ctx context.Context, req DoRequest) (res DoResult, err error) {
	messages := []Message{{
		Kind: MessageKindUser,
		Blocks: []MessageBlock{
			TextBlock{
				Text: req.Task,
			},
		},
	}}
	tools := a.toolSpecs()
	for turn := range cmp.Or(req.MaxTurns, DefaultMaxTurns) {
		c, err := a.complete(ctx, turn, CompleteRequest{
			Messages: messages,
			Tools:    tools,
		})
		// A turn made counts, answered or not: every return below is after
		// one.
		res.Turns = turn + 1
		if err != nil {
			return res, err
		}
		res.Usage = res.Usage.add(c.Usage)
		messages = append(messages, c.Message)

		var results []MessageBlock
		for _, b := range c.Message.Blocks {
			call, ok := b.(ToolCallRequest)
			if !ok {
				continue
			}
			out, err := a.callTool(ctx, call)
			if err != nil {
				out = err.Error()
			}
			results = append(results, ToolCallResult{
				ID:      call.ID,
				Content: out,
				Failure: err != nil,
			})
		}
		if len(results) == 0 {
			res.Output = text(c.Message)
			return res, nil
		}
		messages = append(messages, Message{
			Kind:   MessageKindTool,
			Blocks: results,
		})
	}
	return res, ErrTurnsExceeded
}

func (a *Agent) complete(ctx context.Context, turn int, req CompleteRequest) (CompleteResult, error) {
	done := a.Trace.onTurn(turn)
	c, err := a.Model.Complete(ctx, req)
	done(c, err)
	return c, err
}

func (a *Agent) callTool(ctx context.Context, call ToolCallRequest) (out string, err error) {
	done := a.Trace.onToolCall(call)
	defer func() { done(out, err) }()

	t, has := a.tool(call.Name)
	if !has {
		return "", fmt.Errorf(
			"unknown tool: %q",
			call.Name,
		)
	}
	return t.Call(ctx, call.Input)
}

// text joins the text blocks of a message, one per line.
func text(msg Message) string {
	var sb strings.Builder
	for _, b := range msg.Blocks {
		t, ok := b.(TextBlock)
		if !ok {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(t.Text)
	}
	return sb.String()
}
