package pagerduty

import (
	"errors"
	"log/slog"
	"time"

	"github.com/spoolersh/pagedog/internal/webhook"
)

//go:generate gtrace

// SourceTrace is a set of runtime hooks a [Source] calls as it works.
//
//gtrace:gen
type SourceTrace struct {
	// OnHandle is called when an event arrives, and the func it returns
	// when it is handled, with the number of incidents it made.
	OnHandle func() func(int, error)

	// OnSkipEvent is called for an event of a type that makes no incident.
	OnSkipEvent func(eventType string)
}

// LoggerSourceTrace builds a [SourceTrace] that logs every hook to the
// given logger.
func LoggerSourceTrace(logger *slog.Logger) SourceTrace {
	return SourceTrace{
		OnHandle: func() func(int, error) {
			start := time.Now()
			logger.Debug("handling event")
			return func(n int, err error) {
				logger := logger.With("latency", time.Since(start))
				switch {
				case errors.Is(err, webhook.ErrForbidden):
					logger.Warn("refused event",
						"error", err,
					)
				case err != nil:
					logger.Error("failed handling event",
						"error", err,
					)
				default:
					logger.Info("handled event",
						"incidents", n,
					)
				}
			}
		},
		OnSkipEvent: func(eventType string) {
			logger.Debug("skipped event",
				"type", eventType,
			)
		},
	}
}
