package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/spoolersh/pagedog/internal/ai"
)

// DefaultMaxTokens bounds the tokens one completion may produce when
// [Model.MaxTokens] is zero.
const DefaultMaxTokens = 4096

// DefaultModel is the model asked when [Model.Name] is zero.
const DefaultModel = string(anthropic.ModelClaudeOpus5_5)

var _ ai.Model = (*Model)(nil)

// Model is an [ai.Model] on the Anthropic Messages API.
type Model struct {
	// APIKey is the key the API is asked with.
	//
	// Zero means the SDK's own variable, ANTHROPIC_API_KEY.
	APIKey string

	// BaseURL is where the API is.
	//
	// Zero means the API's own.
	BaseURL string

	// Name is the model to ask, as the API names it: "claude-opus-5-5" and
	// the like.
	//
	// Zero means [DefaultModel].
	Name string

	// MaxTokens bounds the tokens one completion may produce.
	//
	// Zero means [DefaultMaxTokens].
	MaxTokens int

	once   sync.Once
	client anthropic.Client
}

// init makes the client, once.
func (m *Model) init() {
	m.once.Do(func() {
		var opts []option.RequestOption
		if m.APIKey != "" {
			opts = append(opts, option.WithAPIKey(m.APIKey))
		}
		if m.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(m.BaseURL))
		}
		m.client = anthropic.NewClient(opts...)
	})
}

// Complete implements [ai.Model].
func (m *Model) Complete(ctx context.Context, req ai.CompleteRequest) (ai.CompleteResult, error) {
	m.init()
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(cmp.Or(m.Name, DefaultModel)),
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
	resp, err := m.client.Messages.New(ctx, params)
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
