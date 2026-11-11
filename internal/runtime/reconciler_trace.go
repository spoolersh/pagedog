package runtime

import (
	"log/slog"
	"time"
)

//go:generate gtrace

// ReconcilerTrace is a set of runtime hooks a [Reconciler] calls as it
// works.
//
//gtrace:gen
type ReconcilerTrace struct {
	// OnQueueStats is called when the queue is being read, and the func it
	// returns with what it said.
	OnQueueStats func() func(QueueStats, error)

	// OnRuntimeStats is called when the runtime is being read, and the func
	// it returns with what it said.
	OnRuntimeStats func() func(Stats, error)

	// OnLaunch is called when a worker is being launched, and the func it
	// returns when the launch ends.
	OnLaunch func() func(Worker, error)

	// OnShutdown is called when a worker is being shut down, and the func
	// it returns when the shutdown ends.
	OnShutdown func(Worker) func(error)
}

// LoggerReconcilerTrace builds a [ReconcilerTrace] that logs every hook to
// the given logger.
func LoggerReconcilerTrace(logger *slog.Logger) ReconcilerTrace {
	return ReconcilerTrace{
		OnQueueStats: func() func(QueueStats, error) {
			start := time.Now()
			return func(stats QueueStats, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed reading queue stats",
						"error", err,
					)
					return
				}
				logger.Debug("read queue stats",
					"visible", stats.Visible,
					"pending", stats.Pending,
				)
			}
		},
		OnRuntimeStats: func() func(Stats, error) {
			start := time.Now()
			return func(stats Stats, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed reading runtime stats",
						"error", err,
					)
					return
				}
				logger.Debug("read runtime stats",
					"workers", len(stats.Workers),
				)
			}
		},
		OnLaunch: func() func(Worker, error) {
			start := time.Now()
			logger.Debug("launching worker")
			return func(w Worker, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed launching worker",
						"error", err,
					)
					return
				}
				logger.Info("launched worker", slog.Group("worker",
					"id", w.ID,
					"name", w.Name,
				))
			}
		},
		OnShutdown: func(w Worker) func(error) {
			start := time.Now()
			logger := logger.With(slog.Group("worker",
				"id", w.ID,
				"name", w.Name,
			))
			logger.Debug("shutting down worker")
			return func(err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed shutting down worker",
						"error", err,
					)
					return
				}
				logger.Info("shut down worker")
			}
		},
	}
}
