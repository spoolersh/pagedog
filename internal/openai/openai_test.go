package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog/internal/ai"
)

func TestComplete(t *testing.T) {
	// The model calls a tool, with its reasoning, then answers.
	answers := []string{
		`{
			"id": "resp_1",
			"object": "response",
			"status": "completed",
			"output": [
				{"type": "reasoning", "id": "rs_1", "summary": [], "encrypted_content": "enc_1"},
				{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "query_metrics", "arguments": "{\"query\":\"up\"}", "status": "completed"}
			],
			"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
		}`,
		`{
			"id": "resp_2",
			"object": "response",
			"status": "completed",
			"output": [
				{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed", "content": [
					{"type": "output_text", "text": "done", "annotations": []}
				]}
			],
			"usage": {"input_tokens": 20, "output_tokens": 1, "total_tokens": 21}
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
		APIKey:  "test",
		BaseURL: srv.URL,
	}
	tools := []ai.ToolSpec{
		{
			Name: "query_metrics",
			Desc: "runs a PromQL query",
			Input: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"query": map[string]any{"type": "string"}},
				"additionalProperties": false,
			},
		},
	}
	task := ai.Message{
		Kind: ai.MessageKindUser,
		Blocks: []ai.MessageBlock{
			ai.TextBlock{Text: "investigate"},
		},
	}
	ctx := context.Background()

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

	// The second request says the first answer back as it was, reasoning
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
	if exp := DefaultMaxTokens; r.MaxOutputTokens != exp {
		t.Errorf(
			"max_output_tokens: %d; want %d",
			r.MaxOutputTokens, exp,
		)
	}
	if len(r.Tools) != 1 || r.Tools[0].Name != "query_metrics" || r.Tools[0].Strict || r.Tools[0].Params["additionalProperties"] != false {
		t.Errorf(
			"tools: %+v; want query_metrics, not strict, schema kept",
			r.Tools,
		)
	}
	var types []string
	for _, item := range r.Input {
		types = append(types, item.Type+"/"+item.Role)
	}
	// A plain message goes without a type: the API takes it as one.
	if exp := []string{"/user", "reasoning/", "function_call/", "function_call_output/"}; !cmp.Equal(exp, types) {
		t.Fatalf(
			"input: %s",
			cmp.Diff(exp, types),
		)
	}
	if act, exp := r.Input[1].EncryptedContent, "enc_1"; act != exp {
		t.Errorf(
			"reasoning encrypted_content: %q; want %q",
			act, exp,
		)
	}
	if act, exp := r.Input[2].CallID, "call_1"; act != exp {
		t.Errorf(
			"function_call call_id: %q; want %q",
			act, exp,
		)
	}
	if act, exp := r.Input[3].CallID+" "+r.Input[3].Output, "call_1 up 1"; act != exp {
		t.Errorf(
			"function_call_output: %q; want %q",
			act, exp,
		)
	}
}

// request is what the test reads of a request body.
type request struct {
	Model           string `json:"model"`
	Store           bool   `json:"store"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Tools           []struct {
		Type   string         `json:"type"`
		Name   string         `json:"name"`
		Strict bool           `json:"strict"`
		Params map[string]any `json:"parameters"`
	} `json:"tools"`
	Input []struct {
		Type             string `json:"type"`
		Role             string `json:"role"`
		Content          any    `json:"content"`
		ID               string `json:"id"`
		CallID           string `json:"call_id"`
		Name             string `json:"name"`
		Arguments        string `json:"arguments"`
		Output           string `json:"output"`
		EncryptedContent string `json:"encrypted_content"`
	} `json:"input"`
}
