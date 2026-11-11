// Package gemini is an [ai.Model] on the Gemini Interactions API.
package gemini

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/spoolersh/pagedog/internal/ai"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
)

// DefaultMaxTokens bounds the tokens one completion may produce when
// [Model.MaxTokens] is zero.
const DefaultMaxTokens = 4096

// DefaultModel is the model asked when [Model.Name] is zero.
const DefaultModel = "gemini-4-hydrogen"

var _ ai.Model = (*Model)(nil)

// Model is an [ai.Model] on the Gemini Interactions API. The conversation
// is the agent's: nothing is stored on the server.
type Model struct {
	// Client is the Gemini API client, as [genai.NewClient] makes it.
	Client *genai.Client

	// Name is the model to ask, as the API names it.
	//
	// Zero means [DefaultModel].
	Name string

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means [DefaultMaxTokens].
	MaxTokens int

	// replays keeps what the API answered with tool calls, by the first
	// call's id, to say it back as it was: a thinking model wants its
	// thought steps back with the calls' results.
	mu      sync.Mutex
	replays map[string][]interactions.Step
}

// Complete implements [ai.Model].
func (m *Model) Complete(ctx context.Context, req ai.CompleteRequest) (ai.CompleteResult, error) {
	// A result names its call's tool, which only the call says.
	names := make(map[string]string)
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if b, ok := b.(ai.ToolCallRequest); ok {
				names[b.ID] = b.Name
			}
		}
	}
	var steps []interactions.Step
	for _, msg := range req.Messages {
		s, err := m.toSteps(msg, names)
		if err != nil {
			return ai.CompleteResult{}, err
		}
		steps = append(steps, s...)
	}
	input := interactions.NewInteractionsInput(steps)
	body := interactions.CreateModelInteraction{
		Model: interactions.Model(cmp.Or(m.Name, DefaultModel)),
		Input: &input,
		Store: new(false),
		GenerationConfig: &interactions.GenerationConfig{
			MaxOutputTokens: new(cmp.Or(m.MaxTokens, DefaultMaxTokens)),
		},
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, interactions.NewTool(interactions.Function{
			Name:        new(t.Name),
			Description: new(t.Desc),
			Parameters:  t.Input,
		}))
	}
	res, err := m.Client.Interactions.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(body),
	})
	if err != nil {
		return ai.CompleteResult{}, err
	}
	return m.fromInteraction(res.Interaction)
}

// toSteps is a message as steps: a model message that came from the API
// goes back as the API said it.
func (m *Model) toSteps(msg ai.Message, names map[string]string) ([]interactions.Step, error) {
	switch msg.Kind {
	case ai.MessageKindUser:
		var content []interactions.Content
		for _, b := range msg.Blocks {
			b, ok := b.(ai.TextBlock)
			if !ok {
				return nil, fmt.Errorf(
					"gemini: unexpected block %T in a user message",
					b,
				)
			}
			content = append(content, interactions.NewContent(interactions.TextContent{
				Text: b.Text,
			}))
		}
		return []interactions.Step{
			interactions.NewStep(interactions.UserInputStep{
				Content: content,
			}),
		}, nil

	case ai.MessageKindModel:
		if steps, has := m.replay(msg); has {
			return steps, nil
		}
		var (
			content []interactions.Content
			steps   []interactions.Step
		)
		for _, b := range msg.Blocks {
			switch b := b.(type) {
			case ai.TextBlock:
				content = append(content, interactions.NewContent(interactions.TextContent{
					Text: b.Text,
				}))
			case ai.ToolCallRequest:
				var args map[string]any
				if err := json.Unmarshal(b.Input, &args); err != nil {
					return nil, fmt.Errorf(
						"gemini: call %s: %w",
						b.ID, err,
					)
				}
				steps = append(steps, interactions.NewStep(interactions.FunctionCallStep{
					ID:        b.ID,
					Name:      b.Name,
					Arguments: args,
				}))
			default:
				return nil, fmt.Errorf(
					"gemini: unexpected block %T in a model message",
					b,
				)
			}
		}
		if len(content) > 0 {
			steps = append([]interactions.Step{
				interactions.NewStep(interactions.ModelOutputStep{
					Content: content,
				}),
			}, steps...)
		}
		return steps, nil

	case ai.MessageKindTool:
		var steps []interactions.Step
		for _, b := range msg.Blocks {
			b, ok := b.(ai.ToolCallResult)
			if !ok {
				return nil, fmt.Errorf(
					"gemini: unexpected block %T in a tool message",
					b,
				)
			}
			step := interactions.FunctionResultStep{
				CallID: b.ID,
				Result: interactions.NewFunctionResultStepResultUnion(b.Content),
			}
			if name, has := names[b.ID]; has {
				step.Name = new(name)
			}
			if b.Failure {
				step.IsError = new(true)
			}
			steps = append(steps, interactions.NewStep(step))
		}
		return steps, nil

	default:
		return nil, fmt.Errorf(
			"gemini: unexpected message kind %d",
			msg.Kind,
		)
	}
}

// replay is the API's own output for a model message with tool calls, if
// the message came from this model.
func (m *Model) replay(msg ai.Message) ([]interactions.Step, bool) {
	for _, b := range msg.Blocks {
		if b, ok := b.(ai.ToolCallRequest); ok {
			m.mu.Lock()
			defer m.mu.Unlock()
			steps, has := m.replays[b.ID]
			return steps, has
		}
	}
	return nil, false
}

// fromInteraction reads the model's message, and keeps the output as it was
// for the message to go back.
func (m *Model) fromInteraction(in *interactions.Interaction) (ai.CompleteResult, error) {
	if in == nil {
		return ai.CompleteResult{}, errors.New("gemini: no interaction")
	}
	if in.Status == interactions.InteractionStatusFailed {
		return ai.CompleteResult{}, fmt.Errorf(
			"gemini: interaction %s failed",
			value(in.ID),
		)
	}
	msg := ai.Message{
		Kind: ai.MessageKindModel,
	}
	var first string
	for _, step := range in.Steps {
		switch step.Type {
		case interactions.StepTypeModelOutput:
			for _, c := range step.ModelOutputStep.Content {
				if c.TextContent != nil {
					msg.Blocks = append(msg.Blocks, ai.TextBlock{
						Text: c.TextContent.Text,
					})
				}
			}

		case interactions.StepTypeFunctionCall:
			call := step.FunctionCallStep
			// Maps of JSON values: marshaling cannot fail.
			args, _ := json.Marshal(call.Arguments)
			msg.Blocks = append(msg.Blocks, ai.ToolCallRequest{
				ID:    call.ID,
				Name:  call.Name,
				Input: args,
			})
			first = cmp.Or(first, call.ID)
		}
	}
	if first != "" {
		m.mu.Lock()
		if m.replays == nil {
			m.replays = make(map[string][]interactions.Step)
		}
		m.replays[first] = in.Steps
		m.mu.Unlock()
	}
	var usage ai.Usage
	if u := in.Usage; u != nil {
		usage.InputTokens = value(u.TotalInputTokens)
		// Everything that is not input is the model's, thoughts included.
		usage.OutputTokens = cmp.Or(
			value(u.TotalTokens)-usage.InputTokens,
			value(u.TotalOutputTokens),
		)
	}
	return ai.CompleteResult{
		Message: msg,
		Usage:   usage,
	}, nil
}

// value is what p points to, or zero.
func value[T any](p *T) (v T) {
	if p != nil {
		v = *p
	}
	return v
}
