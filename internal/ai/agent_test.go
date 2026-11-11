package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDo(t *testing.T) {
	echo := ToolCallRequest{
		ID:    "1",
		Name:  "echo",
		Input: []byte(`{"a":1}`),
	}
	for _, tc := range []struct {
		name string

		// turns are the model's replies, one per turn; past the last the
		// model fails with errNoTurns.
		turns    []CompleteResult
		maxTurns int

		exp    DoResult
		expErr error

		// expResults are the tool results the model saw after the first
		// turn, if any.
		expResults []MessageBlock
	}{
		{
			name: "answer",
			turns: []CompleteResult{
				modelMessage(TextBlock{
					Text: "done",
				}),
			},
			exp: DoResult{
				Output: "done",
				Turns:  1,
				Usage: Usage{
					InputTokens:  10,
					OutputTokens: 1,
				},
			},
		},
		{
			name: "tool then answer",
			turns: []CompleteResult{
				modelMessage(TextBlock{
					Text: "looking",
				}, echo),
				modelMessage(TextBlock{
					Text: "done",
				}),
			},
			exp: DoResult{
				Output: "done",
				Turns:  2,
				Usage: Usage{
					InputTokens:  20,
					OutputTokens: 2,
				},
			},
			expResults: []MessageBlock{
				ToolCallResult{
					ID:      "1",
					Content: `echo: {"a":1}`,
				},
			},
		},
		{
			name: "tool error goes back to the model",
			turns: []CompleteResult{
				modelMessage(ToolCallRequest{
					ID:    "1",
					Name:  "broken",
					Input: []byte(`{}`),
				}),
				modelMessage(TextBlock{
					Text: "gave up",
				}),
			},
			exp: DoResult{
				Output: "gave up",
				Turns:  2,
				Usage: Usage{
					InputTokens:  20,
					OutputTokens: 2,
				},
			},
			expResults: []MessageBlock{
				ToolCallResult{
					ID:      "1",
					Content: "broken",
					Failure: true,
				},
			},
		},
		{
			name: "unknown tool goes back to the model",
			turns: []CompleteResult{
				modelMessage(ToolCallRequest{
					ID:    "1",
					Name:  "nope",
					Input: []byte(`{}`),
				}),
				modelMessage(TextBlock{
					Text: "ok",
				}),
			},
			exp: DoResult{
				Output: "ok",
				Turns:  2,
				Usage: Usage{
					InputTokens:  20,
					OutputTokens: 2,
				},
			},
			expResults: []MessageBlock{
				ToolCallResult{
					ID:      "1",
					Content: `unknown tool: "nope"`,
					Failure: true,
				},
			},
		},
		{
			name: "max turns",
			turns: []CompleteResult{
				modelMessage(echo),
				modelMessage(echo),
				modelMessage(TextBlock{
					Text: "never seen",
				}),
			},
			maxTurns: 2,
			exp: DoResult{
				Turns: 2,
				Usage: Usage{
					InputTokens:  20,
					OutputTokens: 2,
				},
			},
			expErr: ErrTurnsExceeded,
		},
		{
			name: "model error",
			exp: DoResult{
				Turns: 1,
			},
			expErr: errNoTurns,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reqs []CompleteRequest
			turns := tc.turns
			a := &Agent{
				Model: &stubModel{
					DoComplete: func(_ context.Context, req CompleteRequest) (CompleteResult, error) {
						reqs = append(reqs, req)
						if len(turns) == 0 {
							return CompleteResult{}, errNoTurns
						}
						c := turns[0]
						turns = turns[1:]
						return c, nil
					},
				},
			}
			tools := []Tool{
				&stubTool{
					DoSpec: func() ToolSpec {
						return ToolSpec{
							Name: "echo",
						}
					},
					DoCall: func(_ context.Context, input []byte) (string, error) {
						return "echo: " + string(input), nil
					},
				},
				&stubTool{
					DoSpec: func() ToolSpec {
						return ToolSpec{
							Name: "broken",
						}
					},
					DoCall: func(context.Context, []byte) (string, error) {
						return "", errors.New("broken")
					},
				},
			}
			for _, tool := range tools {
				if err := a.RegisterTool(tool); err != nil {
					t.Fatal(err)
				}
			}
			act, err := a.Do(context.Background(), DoRequest{
				Task:     "task",
				MaxTurns: tc.maxTurns,
			})
			if !errors.Is(err, tc.expErr) {
				t.Fatalf(
					"Do(): %v; want %v",
					err, tc.expErr,
				)
			}
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"result: %s",
					cmp.Diff(tc.exp, act),
				)
			}

			// The first request carries the task, and every tool by name.
			first := reqs[0]
			expTask := Message{
				Kind: MessageKindUser,
				Blocks: []MessageBlock{
					TextBlock{
						Text: "task",
					},
				},
			}
			if !cmp.Equal(expTask, first.Messages[0]) {
				t.Errorf(
					"task: %s",
					cmp.Diff(expTask, first.Messages[0]),
				)
			}
			expNames := []string{"broken", "echo"}
			var actNames []string
			for _, d := range first.Tools {
				actNames = append(actNames, d.Name)
			}
			if !cmp.Equal(expNames, actNames) {
				t.Errorf(
					"tools: %s",
					cmp.Diff(expNames, actNames),
				)
			}
			if tc.expResults != nil {
				// Task, model, results.
				last := reqs[len(reqs)-1]
				actResults := last.Messages[2].Blocks
				if !cmp.Equal(tc.expResults, actResults) {
					t.Errorf(
						"results: %s",
						cmp.Diff(tc.expResults, actResults),
					)
				}
			}
		})
	}
}

func TestRegisterToolTwice(t *testing.T) {
	var a Agent
	tool := &stubTool{
		DoSpec: func() ToolSpec {
			return ToolSpec{
				Name: "x",
			}
		},
	}
	if err := a.RegisterTool(tool); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterTool(tool); err == nil {
		t.Fatal("want error; got nothing")
	}
}

func TestRegisterToolUnnamed(t *testing.T) {
	var a Agent
	if err := a.RegisterTool(&stubTool{}); err == nil {
		t.Fatal("want error; got nothing")
	}
}

type stubModel struct {
	DoComplete func(context.Context, CompleteRequest) (CompleteResult, error)
}

var _ Model = (*stubModel)(nil)

func (s *stubModel) Complete(ctx context.Context, req CompleteRequest) (CompleteResult, error) {
	if s.DoComplete != nil {
		return s.DoComplete(ctx, req)
	}
	return CompleteResult{}, errors.New("stub: Complete not implemented")
}

type stubTool struct {
	DoSpec func() ToolSpec
	DoCall func(context.Context, []byte) (string, error)
}

var _ Tool = (*stubTool)(nil)

func (s *stubTool) Spec() ToolSpec {
	if s.DoSpec != nil {
		return s.DoSpec()
	}
	return ToolSpec{}
}

func (s *stubTool) Call(ctx context.Context, input []byte) (string, error) {
	if s.DoCall != nil {
		return s.DoCall(ctx, input)
	}
	return "", errors.New("stub: Call not implemented")
}

var errNoTurns = errors.New("no more turns")

// modelMessage is a model's reply made of blocks, with the same usage every
// time.
func modelMessage(blocks ...MessageBlock) CompleteResult {
	return CompleteResult{
		Message: Message{
			Kind:   MessageKindModel,
			Blocks: blocks,
		},
		Usage: Usage{
			InputTokens:  10,
			OutputTokens: 1,
		},
	}
}
