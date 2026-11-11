package webhook

import (
	"log/slog"
	"time"

	"github.com/spoolersh/pagedog"
)

//go:generate gtrace

// ServerTrace is a set of runtime hooks a [Server] calls as it
// works.
//
//gtrace:gen
type ServerTrace struct {
	// OnUnknownSource is called when a request names a source that is not
	// registered.
	OnUnknownSource func(name string)

	// OnSend is called when an incident is being sent to the queue, and the
	// func it returns when the send ends.
	OnSend func(pagedog.Incident) func(error)
}

// LoggerServerTrace builds a [ServerTrace] that logs every hook to the given
// logger.
func LoggerServerTrace(logger *slog.Logger) ServerTrace {
	return ServerTrace{
		OnUnknownSource: func(name string) {
			logger.Warn("unknown source",
				"source", name,
			)
		},
		OnSend: func(inc pagedog.Incident) func(error) {
			start := time.Now()
			logger := logger.With(slog.Group("incident",
				"source", inc.Source,
				"id", inc.ID,
				"key", inc.Key,
				"severity", inc.Severity,
				"title", inc.Title,
			))
			logger.Debug("sending incident")
			return func(err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed sending incident",
						"error", err,
					)
					return
				}
				logger.Info("sent incident")
			}
		},
	}
}
