package alertmanager

import (
	"errors"
	"log/slog"
	"time"

	pagedog_webhook "github.com/spoolersh/pagedog/internal/webhook"
)

//go:generate gtrace

// SourceTrace is a set of runtime hooks a [Source] calls as it works.
//
//gtrace:gen
type SourceTrace struct {
	// OnHandle is called when a notification arrives, and the func it
	// returns when it is handled, with the number of incidents it made.
	OnHandle func() func(int, error)

	// OnSkipAlert is called for an alert that makes no incident, with its
	// fingerprint and status.
	OnSkipAlert func(fingerprint, status string)
}

// LoggerSourceTrace builds a [SourceTrace] that logs every hook to the
// given logger.
func LoggerSourceTrace(logger *slog.Logger) SourceTrace {
	return SourceTrace{
		OnHandle: func() func(int, error) {
			start := time.Now()
			logger.Debug("handling notification")
			return func(n int, err error) {
				logger := logger.With("latency", time.Since(start))
				switch {
				case errors.Is(err, pagedog_webhook.ErrForbidden):
					logger.Warn("refused notification",
						"error", err,
					)
				case err != nil:
					logger.Error("failed handling notification",
						"error", err,
					)
				default:
					logger.Info("handled notification",
						"incidents", n,
					)
				}
			}
		},
		OnSkipAlert: func(fingerprint, status string) {
			logger.Debug("skipped alert",
				"fingerprint", fingerprint,
				"status", status,
			)
		},
	}
}
