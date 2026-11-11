package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog/internal/ai"
	"google.golang.org/genai"
)

func TestComplete(t *testing.T) {
	// The model calls a tool, with its thoughts, then answers.
	answers := []string{
		`{
			"id": "int_1",
			"model": "gemini-4-hydrogen",
			"created": "2026-11-13T10:00:00Z",
			"status": "completed",
			"steps": [
				{"type": "thought", "signature": "sig_1", "summary": []},
				{"type": "function_call", "id": "call_1", "name": "query_metrics", "arguments": {"query": "up"}}
			],
			"usage": {"total_input_tokens": 10, "total_output_tokens": 5, "total_thought_tokens": 3, "total_tokens": 18}
		}`,
		`{
			"id": "int_2",
			"model": "gemini-4-hydrogen",
			"created": "2026-11-13T10:00:05Z",
			"status": "completed",
			"steps": [
				{"type": "model_output", "content": [{"type": "text", "text": "done"}]}
			],
			"usage": {"total_input_tokens": 20, "total_output_tokens": 1, "total_tokens": 21}
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

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  "test",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: srv.URL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{
		Client: client,
	}
	tools := []ai.ToolSpec{
		{
			Name: "query_metrics",
			Desc: "runs a PromQL query",
			Input: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
			},
		},
	}
	task := ai.Message{
		Kind: ai.MessageKindUser,
		Blocks: []ai.MessageBlock{
			ai.TextBlock{Text: "investigate"},
		},
	}

	first, err := m.Complete(ctx, ai.CompleteRequest{
		Messages: []ai.Message{task},
		Tools:    tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	exp := ai.CompleteResult{
		Message: ai.Message{
			Kind: ai.MessageKindModel,
			Blocks: []ai.MessageBlock{
				ai.ToolCallRequest{
					ID:    "call_1",
					Name:  "query_metrics",
					Input: json.RawMessage(`{"query":"up"}`),
				},
			},
		},
		Usage: ai.Usage{
			InputTokens:  10,
			OutputTokens: 8,
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
			task,
			first.Message,
			{
				Kind: ai.MessageKindTool,
				Blocks: []ai.MessageBlock{
					ai.ToolCallResult{ID: "call_1", Content: "up 1"},
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
				ai.TextBlock{Text: "done"},
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

	// The second request says the first answer back as it was, thoughts
	// included, then the tool's result.
	if len(requests) != 2 {
		t.Fatalf(
			"requests: %d; want 2",
			len(requests),
		)
	}
	r := requests[1]
	if exp := DefaultModel; r.Model != exp {
		t.Errorf(
			"model: %q; want %q",
			r.Model, exp,
		)
	}
	if r.Store {
		t.Error("store: true; want false")
	}
	if exp := DefaultMaxTokens; r.GenerationConfig.MaxOutputTokens != exp {
		t.Errorf(
			"max_output_tokens: %d; want %d",
			r.GenerationConfig.MaxOutputTokens, exp,
		)
	}
	if len(r.Tools) != 1 || r.Tools[0].Type != "function" || r.Tools[0].Name != "query_metrics" || r.Tools[0].Params["type"] != "object" {
		t.Errorf(
			"tools: %+v; want one function, query_metrics, schema kept",
			r.Tools,
		)
	}
	var types []string
	for _, step := range r.Input {
		types = append(types, step.Type)
	}
	if exp := []string{"user_input", "thought", "function_call", "function_result"}; !cmp.Equal(exp, types) {
		t.Fatalf(
			"input: %s",
			cmp.Diff(exp, types),
		)
	}
	if act, exp := r.Input[1].Signature, "sig_1"; act != exp {
		t.Errorf(
			"thought signature: %q; want %q",
			act, exp,
		)
	}
	if act, exp := r.Input[2].ID, "call_1"; act != exp {
		t.Errorf(
			"function_call id: %q; want %q",
			act, exp,
		)
	}
	if act, exp := r.Input[3].CallID+" "+r.Input[3].Name, "call_1 query_metrics"; act != exp {
		t.Errorf(
			"function_result: %q; want %q",
			act, exp,
		)
	}
	if act, exp := r.Input[3].Result, any("up 1"); act != exp {
		t.Errorf(
			"function_result result: %v; want %v",
			act, exp,
		)
	}
}

// request is what the test reads of a request body.
type request struct {
	Model            string `json:"model"`
	Store            bool   `json:"store"`
	GenerationConfig struct {
		MaxOutputTokens int `json:"max_output_tokens"`
	} `json:"generation_config"`
	Tools []struct {
		Type   string         `json:"type"`
		Name   string         `json:"name"`
		Params map[string]any `json:"parameters"`
	} `json:"tools"`
	Input []struct {
		Type      string         `json:"type"`
		Content   []any          `json:"content"`
		ID        string         `json:"id"`
		CallID    string         `json:"call_id"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Result    any            `json:"result"`
		Signature string         `json:"signature"`
		IsError   bool           `json:"is_error"`
	} `json:"input"`
}
