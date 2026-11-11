package exec

import (
	"log/slog"
	"time"
)

//go:generate gtrace

// RuntimeTrace is a set of runtime hooks a [Runtime] calls as it works.
//
//gtrace:gen
type RuntimeTrace struct {
	// OnRun is called when a worker's process has started, with its pid,
	// and the func it returns when it exits, with the exit error if any: an
	// [std_exec.ExitError] for a non-zero exit code, including one killed
	// by a shutdown.
	OnRun func(pid int) func(error)
}

// LoggerRuntimeTrace builds a [RuntimeTrace] that logs every hook to the
// given logger.
func LoggerRuntimeTrace(logger *slog.Logger) RuntimeTrace {
	return RuntimeTrace{
		OnRun: func(pid int) func(error) {
			start := time.Now()
			logger := logger.With(slog.Group("worker",
				"pid", pid,
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
