package slack

import (
	"log/slog"
	"time"

	"github.com/spoolersh/pagedog"
)

//go:generate gtrace

// NotifierTrace is a set of runtime hooks a [Notifier] calls as it works.
//
//gtrace:gen
type NotifierTrace struct {
	// OnNotify is called when a report is being posted, and the func it
	// returns when it has been, or could not be.
	OnNotify func(r pagedog.IncidentReport) func(error)
}

// LoggerNotifierTrace builds a [NotifierTrace] that logs every hook to the
// given logger.
func LoggerNotifierTrace(logger *slog.Logger) NotifierTrace {
	return NotifierTrace{
		OnNotify: func(r pagedog.IncidentReport) func(error) {
			start := time.Now()
			logger := logger.With("key", r.Incident.Key)
			logger.Debug("posting report")
			return func(err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed posting report",
						"error", err,
					)
					return
				}
				logger.Info("posted report")
			}
		},
	}
}
