package docker

import (
	"log/slog"
	"time"
)

//go:generate gtrace

// Trace is a set of runtime hooks a [Runtime] calls as it works, one per
// engine operation.
//
//gtrace:gen
type Trace struct {
	// OnList is called when containers are being listed, and the func it
	// returns with how many were found.
	OnList func() func(int, error)

	// OnCreate is called when a container is being created under the name,
	// and the func it returns with the id it got.
	OnCreate func(name string) func(id string, err error)

	OnStart  func(id string) func(error)
	OnStop   func(id string) func(error)
	OnRemove func(id string) func(error)
}

// LoggerTrace builds a [Trace] that logs every hook to the given logger.
func LoggerTrace(logger *slog.Logger) Trace {
	op := func(name string) func(string) func(error) {
		return func(id string) func(error) {
			start := time.Now()
			logger := logger.With("id", id)
			logger.Debug(name + " container")
			return func(err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed "+name+" container",
						"error", err,
					)
					return
				}
				logger.Debug("done " + name + " container")
			}
		}
	}
	return Trace{
		OnList: func() func(int, error) {
			start := time.Now()
			logger.Debug("listing containers")
			return func(n int, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed listing containers",
						"error", err,
					)
					return
				}
				logger.Debug("done listing containers",
					"containers", n,
				)
			}
		},
		OnCreate: func(name string) func(string, error) {
			start := time.Now()
			logger := logger.With("name", name)
			logger.Debug("creating container")
			return func(id string, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed creating container",
						"error", err,
					)
					return
				}
				logger.Debug("done creating container",
					"id", id,
				)
			}
		},
		OnStart:  op("starting"),
		OnStop:   op("stopping"),
		OnRemove: op("removing"),
	}
}
