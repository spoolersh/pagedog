package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog/internal/ai"
)

func TestComplete(t *testing.T) {
	// The model calls a tool, then answers.
	answers := []string{
		`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-opus-5-5",
			"content": [
				{"type": "text", "text": "let me check"},
				{"type": "tool_use", "id": "toolu_1", "name": "query_metrics", "input": {"query":"up"}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`,
		`{
			"id": "msg_2",
			"type": "message",
			"role": "assistant",
			"model": "claude-opus-5-5",
			"content": [
				{"type": "text", "text": "done"}
			],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 20, "output_tokens": 1}
		}`,
	}
	var requests []request
	srv := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var r request
		if err := json.Unmarshal(body, &r); err != nil {
			t.Errorf("request: %v", err)
		}
		requests = append(requests, r)
		res.Header().Set("Content-Type", "application/json")
		io.WriteString(res, answers[len(requests)-1])
	}))
	defer srv.Close()

	m := &Model{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	}
	tools := []ai.ToolSpec{
		{
			Name: "query_metrics",
			Desc: "Runs a PromQL query.",
			Input: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type": "string",
					},
				},
			},
		},
	}
	ask := ai.Message{
		Kind: ai.MessageKindUser,
		Blocks: []ai.MessageBlock{
			ai.TextBlock{
				Text: "What is up?",
			},
		},
	}
	ctx := context.Background()

	first, err := m.Complete(ctx, ai.CompleteRequest{
		Messages: []ai.Message{ask},
		Tools:    tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	exp := ai.CompleteResult{
		Message: ai.Message{
			Kind: ai.MessageKindModel,
			Blocks: []ai.MessageBlock{
				ai.TextBlock{
					Text: "let me check",
				},
				ai.ToolCallRequest{
					ID:    "toolu_1",
					Name:  "query_metrics",
					Input: json.RawMessage(`{"query":"up"}`),
				},
			},
		},
		Usage: ai.Usage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}
	if !cmp.Equal(exp, first) {
		t.Errorf(
			"first result: %s",
			cmp.Diff(exp, first),
		)
	}

	second, err := m.Complete(ctx, ai.CompleteRequest{
		Messages: []ai.Message{
			ask,
			first.Message,
			{
				Kind: ai.MessageKindTool,
				Blocks: []ai.MessageBlock{
					ai.ToolCallResult{
						ID:      "toolu_1",
						Content: "3 series",
					},
				},
			},
		},
		Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	exp = ai.CompleteResult{
		Message: ai.Message{
			Kind: ai.MessageKindModel,
			Blocks: []ai.MessageBlock{
				ai.TextBlock{
					Text: "done",
				},
			},
		},
		Usage: ai.Usage{
			InputTokens:  20,
			OutputTokens: 1,
		},
	}
	if !cmp.Equal(exp, second) {
		t.Errorf(
			"second result: %s",
			cmp.Diff(exp, second),
		)
	}

	if act, exp := len(requests), 2; act != exp {
		t.Fatalf(
			"requests: %d; want %d",
			act, exp,
		)
	}
	// The defaults go on the wire, and the tool with its schema as given.
	if act, exp := requests[0].Model, string(DefaultModel); act != exp {
		t.Errorf(
			"model: %q; want %q",
			act, exp,
		)
	}
	if act, exp := requests[0].MaxTokens, DefaultMaxTokens; act != exp {
		t.Errorf(
			"max tokens: %d; want %d",
			act, exp,
		)
	}
	if act, exp := len(requests[0].Tools), 1; act != exp {
		t.Fatalf(
			"tools: %d; want %d",
			act, exp,
		)
	}
	if act, exp := requests[0].Tools[0].Name, "query_metrics"; act != exp {
		t.Errorf(
			"tool name: %q; want %q",
			act, exp,
		)
	}
	if act, exp := requests[0].Tools[0].Description, "Runs a PromQL query."; act != exp {
		t.Errorf(
			"tool description: %q; want %q",
			act, exp,
		)
	}
	if act, exp := string(requests[0].Tools[0].InputSchema), `"type":"object"`; !strings.Contains(act, exp) {
		t.Errorf(
			"tool schema: %s; want it to contain %s",
			act, exp,
		)
	}
	// The conversation goes back as the API wants it: the model's turn as
	// the assistant, the tool's result in a user message.
	var messages []message
	for _, m := range requests[1].Messages {
		msg := message{
			Role: m.Role,
		}
		for _, c := range m.Content {
			msg.Blocks = append(msg.Blocks, block{
				Type:      c.Type,
				Text:      c.Text,
				ID:        c.ID,
				Name:      c.Name,
				Input:     string(c.Input),
				ToolUseID: c.ToolUseID,
				IsError:   c.IsError,
			})
		}
		messages = append(messages, msg)
	}
	expMessages := []message{
		{
			Role: "user",
			Blocks: []block{
				{
					Type: "text",
					Text: "What is up?",
				},
			},
		},
		{
			Role: "assistant",
			Blocks: []block{
				{
					Type: "text",
					Text: "let me check",
				},
				{
					Type:  "tool_use",
					ID:    "toolu_1",
					Name:  "query_metrics",
					Input: `{"query":"up"}`,
				},
			},
		},
		{
			Role: "user",
			Blocks: []block{
				{
					Type:      "tool_result",
					ToolUseID: "toolu_1",
				},
			},
		},
	}
	if !cmp.Equal(expMessages, messages) {
		t.Errorf(
			"messages: %s",
			cmp.Diff(expMessages, messages),
		)
	}
	if act, exp := string(requests[1].Messages[2].Content[0].Content), "3 series"; !strings.Contains(act, exp) {
		t.Errorf(
			"tool result: %s; want it to contain %q",
			act, exp,
		)
	}
}

// request is what the test reads of a request body.
type request struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Tools     []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	} `json:"tools"`
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"messages"`
}

// message is a request's message as the test compares it: a tool result's
// content aside, which the SDK lays out its own way and is checked apart.
type message struct {
	Role   string
	Blocks []block
}

type block struct {
	Type      string
	Text      string
	ID        string
	Name      string
	Input     string
	ToolUseID string
	IsError   bool
}
