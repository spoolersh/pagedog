package goro

import (
	"log/slog"
	"time"
)

//go:generate gtrace

// Trace is a set of runtime hooks a [Runtime] calls as it works.
//
//gtrace:gen
type Trace struct {
	// OnRun is called when a worker's goroutine starts, and the func it
	// returns when the worker returns, with what it returned. There is no
	// log to read afterwards, so this is where a worker's failure shows.
	OnRun func(uint64) func(error)
}

// LoggerTrace builds a [Trace] that logs every hook to the given logger.
func LoggerTrace(logger *slog.Logger) Trace {
	return Trace{
		OnRun: func(id uint64) func(error) {
			start := time.Now()
			logger := logger.With(slog.Group("worker",
				"id", id,
			))
			logger.Debug("running worker")
			return func(err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("worker failed",
						"error", err,
					)
					return
				}
				logger.Debug("worker exited")
			}
		},
	}
}
