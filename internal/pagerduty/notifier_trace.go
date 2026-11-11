package pagerduty

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
	// OnNotify is called when a report is being shared, and the func it
	// returns when it has been, or could not be.
	OnNotify func(r pagedog.IncidentReport) func(error)

	// OnSkip is called for a report on an incident from another source.
	OnSkip func(r pagedog.IncidentReport)
}

// LoggerNotifierTrace builds a [NotifierTrace] that logs every hook to the
// given logger.
func LoggerNotifierTrace(logger *slog.Logger) NotifierTrace {
	return NotifierTrace{
		OnNotify: func(r pagedog.IncidentReport) func(error) {
			start := time.Now()
			logger := logger.With("key", r.Incident.Key)
			logger.Debug("sharing report")
			return func(err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed sharing report",
						"error", err,
					)
					return
				}
				logger.Info("shared report")
			}
		},
		OnSkip: func(r pagedog.IncidentReport) {
			logger.Debug("skipped report from another source",
				"key", r.Incident.Key,
				"source", r.Incident.Source,
			)
		},
	}
}
