package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/spoolersh/pagedog/internal/ai"
)

// DefaultMaxTokens bounds the tokens one completion may produce when
// [Model.MaxTokens] is zero.
const DefaultMaxTokens = 4096

// DefaultModel is the model asked when [Model.Name] is zero.
const DefaultModel = anthropic.ModelClaudeOpus5_5

var _ ai.Model = (*Model)(nil)

// Model is an [ai.Model] on the Anthropic Messages API.
type Model struct {
	// Client is the Anthropic API client, as [anthropic.NewClient] makes it.
	Client anthropic.Client

	// Name is the model to ask, as the API names it:
	// [anthropic.ModelClaudeOpus5_5] and the like.
	//
	// Zero means [DefaultModel].
	Name anthropic.Model

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means [DefaultMaxTokens].
	MaxTokens int
}

// Complete implements [ai.Model].
func (m *Model) Complete(ctx context.Context, req ai.CompleteRequest) (ai.CompleteResult, error) {
	params := anthropic.MessageNewParams{
		Model:     cmp.Or(m.Name, DefaultModel),
		MaxTokens: int64(cmp.Or(m.MaxTokens, DefaultMaxTokens)),
	}
	for _, msg := range req.Messages {
		p, err := toMessage(msg)
		if err != nil {
			return ai.CompleteResult{}, err
		}
		params.Messages = append(params.Messages, p)
	}
	for _, t := range req.Tools {
		schema, err := toInputSchema(t.Input)
		if err != nil {
			return ai.CompleteResult{}, fmt.Errorf(
				"tool %s: %w",
				t.Name, err,
			)
		}
		params.Tools = append(params.Tools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Desc),
				InputSchema: schema,
			},
		})
	}
	resp, err := m.Client.Messages.New(ctx, params)
	if err != nil {
		return ai.CompleteResult{}, err
	}
	return fromMessage(resp), nil
}

// toInputSchema carries a tool's input schema into the API's parameter as it
// is, every keyword kept, the type set to object.
func toInputSchema(v any) (anthropic.ToolInputSchemaParam, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return anthropic.ToolInputSchemaParam{}, err
	}
	var p anthropic.ToolInputSchemaParam
	if err := json.Unmarshal(data, &p); err != nil {
		return anthropic.ToolInputSchemaParam{}, err
	}
	return p, nil
}

func toMessage(msg ai.Message) (anthropic.MessageParam, error) {
	blocks := make([]anthropic.ContentBlockParamUnion, len(msg.Blocks))
	for i, b := range msg.Blocks {
		switch b := b.(type) {
		case ai.TextBlock:
			blocks[i] = anthropic.NewTextBlock(b.Text)

		case ai.ToolCallRequest:
			blocks[i] = anthropic.NewToolUseBlock(b.ID, b.Input, b.Name)

		case ai.ToolCallResult:
			blocks[i] = anthropic.NewToolResultBlock(b.ID, b.Content, b.Failure)

		default:
			return anthropic.MessageParam{}, fmt.Errorf(
				"claude: unexpected block %T",
				b,
			)
		}
	}
	switch msg.Kind {
	case
		// Tool results travel in a user message here.
		ai.MessageKindTool,
		ai.MessageKindUser:
		return anthropic.NewUserMessage(blocks...), nil

	case ai.MessageKindModel:
		return anthropic.NewAssistantMessage(blocks...), nil

	default:
		return anthropic.MessageParam{}, fmt.Errorf(
			"claude: unexpected message kind %d",
			msg.Kind,
		)
	}
}

func fromMessage(resp *anthropic.Message) ai.CompleteResult {
	msg := ai.Message{
		Kind: ai.MessageKindModel,
	}
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			msg.Blocks = append(msg.Blocks, ai.TextBlock{
				Text: b.Text,
			})

		case anthropic.ToolUseBlock:
			msg.Blocks = append(msg.Blocks, ai.ToolCallRequest{
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Input,
			})
		}
	}
	return ai.CompleteResult{
		Message: msg,
		Usage: ai.Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}
}
