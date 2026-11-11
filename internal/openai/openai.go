// Package openai is an [ai.Model] on the OpenAI Responses API.
package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/spoolersh/pagedog/internal/ai"
)

// DefaultMaxTokens bounds the tokens one completion may produce when
// [Model.MaxTokens] is zero.
const DefaultMaxTokens = 4096

// DefaultModel is the model asked when [Model.Name] is zero.
const DefaultModel = "gpt-6-astra"

var _ ai.Model = (*Model)(nil)

// Model is an [ai.Model] on the OpenAI Responses API. The conversation is
// the agent's: nothing is stored on the server.
type Model struct {
	// Client is the OpenAI API client, as [openai.NewClient] makes it.
	Client openai.Client

	// Name is the model to ask, as the API names it.
	//
	// Zero means [DefaultModel].
	Name string

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means [DefaultMaxTokens].
	MaxTokens int

	// replays keeps what the API answered with tool calls, by the first
	// call's id, to say it back as it was: a reasoning model wants its
	// reasoning items back with the calls' results.
	mu      sync.Mutex
	replays map[string][]responses.ResponseInputItemUnionParam
}

// Complete implements [ai.Model].
func (m *Model) Complete(ctx context.Context, req ai.CompleteRequest) (ai.CompleteResult, error) {
	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(cmp.Or(m.Name, DefaultModel)),
		MaxOutputTokens: openai.Int(int64(cmp.Or(m.MaxTokens, DefaultMaxTokens))),
		Store:           openai.Bool(false),
	}
	var input responses.ResponseInputParam
	for _, msg := range req.Messages {
		items, err := m.toItems(msg)
		if err != nil {
			return ai.CompleteResult{}, err
		}
		input = append(input, items...)
	}
	params.Input = responses.ResponseNewParamsInputUnion{
		OfInputItemList: input,
	}
	for _, t := range req.Tools {
		parameters, err := toParameters(t.Input)
		if err != nil {
			return ai.CompleteResult{}, fmt.Errorf(
				"tool %s: %w",
				t.Name, err,
			)
		}
		params.Tools = append(params.Tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        t.Name,
				Description: openai.String(t.Desc),
				Parameters:  parameters,
				// Strict mode takes a subset of JSON Schema only, and the
				// tools describe themselves in all of it.
				Strict: openai.Bool(false),
			},
		})
	}
	resp, err := m.Client.Responses.New(ctx, params)
	if err != nil {
		return ai.CompleteResult{}, err
	}
	return m.fromResponse(resp), nil
}

// toParameters carries a tool's input schema into the API's parameter as it
// is, every keyword kept.
func toParameters(v any) (shared.FunctionParameters, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var p shared.FunctionParameters
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return p, nil
}

// toItems is a message as input items: a model message that came from the
// API goes back as the API said it.
func (m *Model) toItems(msg ai.Message) ([]responses.ResponseInputItemUnionParam, error) {
	var role responses.EasyInputMessageRole
	switch msg.Kind {
	case ai.MessageKindUser:
		role = responses.EasyInputMessageRoleUser
	case ai.MessageKindModel:
		if items, has := m.replay(msg); has {
			return items, nil
		}
		role = responses.EasyInputMessageRoleAssistant
	case ai.MessageKindTool:
	default:
		return nil, fmt.Errorf(
			"openai: unexpected message kind %d",
			msg.Kind,
		)
	}
	items := make([]responses.ResponseInputItemUnionParam, 0, len(msg.Blocks))
	for _, b := range msg.Blocks {
		switch b := b.(type) {
		case ai.TextBlock:
			items = append(items, responses.ResponseInputItemParamOfMessage(b.Text, role))

		case ai.ToolCallRequest:
			items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(b.Input), b.ID, b.Name))

		case ai.ToolCallResult:
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: openai.String(b.ID),
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: openai.String(b.Content),
					},
				},
			})

		default:
			return nil, fmt.Errorf(
				"openai: unexpected block %T",
				b,
			)
		}
	}
	return items, nil
}

// replay is the API's own output for a model message with tool calls, if
// the message came from this model.
func (m *Model) replay(msg ai.Message) ([]responses.ResponseInputItemUnionParam, bool) {
	for _, b := range msg.Blocks {
		if b, ok := b.(ai.ToolCallRequest); ok {
			m.mu.Lock()
			defer m.mu.Unlock()
			items, has := m.replays[b.ID]
			return items, has
		}
	}
	return nil, false
}

// fromResponse reads the model's message, and keeps the output as it was
// for the message to go back.
func (m *Model) fromResponse(resp *responses.Response) ai.CompleteResult {
	msg := ai.Message{
		Kind: ai.MessageKindModel,
	}
	var (
		replay []responses.ResponseInputItemUnionParam
		first  string
	)
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			message := item.AsMessage()
			for _, c := range message.Content {
				switch c.Type {
				case "output_text":
					msg.Blocks = append(msg.Blocks, ai.TextBlock{
						Text: c.Text,
					})
				case "refusal":
					msg.Blocks = append(msg.Blocks, ai.TextBlock{
						Text: c.Refusal,
					})
				}
			}
			p := message.ToParam()
			replay = append(replay, responses.ResponseInputItemUnionParam{
				OfOutputMessage: &p,
			})

		case "function_call":
			call := item.AsFunctionCall()
			msg.Blocks = append(msg.Blocks, ai.ToolCallRequest{
				ID:    call.CallID,
				Name:  call.Name,
				Input: json.RawMessage(call.Arguments),
			})
			first = cmp.Or(first, call.CallID)
			p := call.ToParam()
			replay = append(replay, responses.ResponseInputItemUnionParam{
				OfFunctionCall: &p,
			})

		case "reasoning":
			p := item.AsReasoning().ToParam()
			replay = append(replay, responses.ResponseInputItemUnionParam{
				OfReasoning: &p,
			})
		}
	}
	if first != "" {
		m.mu.Lock()
		if m.replays == nil {
			m.replays = make(map[string][]responses.ResponseInputItemUnionParam)
		}
		m.replays[first] = replay
		m.mu.Unlock()
	}
	return ai.CompleteResult{
		Message: msg,
		Usage: ai.Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}
}
