package runtime

import (
	"log/slog"
	"time"
)

//go:generate gtrace

// GoroutinesTrace is a set of runtime hooks a [Goroutines] calls as it works.
//
//gtrace:gen
type GoroutinesTrace struct {
	// OnRun is called when a worker's goroutine starts, and the func it
	// returns when the worker returns, with what it returned, a recovered
	// panic included. There is no log to read afterwards, so this is where
	// a worker's failure shows.
	OnRun func(Worker) func(error)
}

// LoggerGoroutinesTrace builds a [GoroutinesTrace] that logs every hook to
// the given logger.
func LoggerGoroutinesTrace(logger *slog.Logger) GoroutinesTrace {
	return GoroutinesTrace{
		OnRun: func(w Worker) func(error) {
			start := time.Now()
			logger := logger.With(slog.Group("worker",
				"id", w.ID,
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
