package ai

import (
	"log/slog"
	"time"
)

//go:generate gtrace

// AgentTrace is a set of runtime hooks an [Agent] calls as it works.
//
//gtrace:gen
type AgentTrace struct {
	// OnTurn is called when the model is asked, with the turn's number from
	// one, and the func it returns when the model has replied.
	OnTurn func(turn int) func(CompleteResult, error)

	// OnToolCall is called when a tool the model called is run, and the func
	// it returns when it is done, with its output or its error.
	OnToolCall func(ToolCallRequest) func(string, error)
}

// LoggerAgentTrace builds a [AgentTrace] that logs every hook to the given
// logger.
func LoggerAgentTrace(logger *slog.Logger) AgentTrace {
	return AgentTrace{
		OnTurn: func(turn int) func(CompleteResult, error) {
			start := time.Now()
			logger := logger.With("turn", turn)
			logger.Debug("asking model")
			return func(c CompleteResult, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed asking model",
						"error", err,
					)
					return
				}
				logger.Info("model replied",
					"input_tokens", c.Usage.InputTokens,
					"output_tokens", c.Usage.OutputTokens,
				)
			}
		},
		OnToolCall: func(call ToolCallRequest) func(string, error) {
			start := time.Now()
			logger := logger.With(slog.Group("tool",
				"id", call.ID,
				"name", call.Name,
				"input", string(call.Input),
			))
			logger.Debug("calling tool")
			return func(out string, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					// The error goes back to the model, which may recover: a
					// warning, not a failure.
					logger.Warn("tool failed",
						"error", err,
					)
					return
				}
				logger.Info("called tool",
					"output_len", len(out),
				)
			}
		},
	}
}
