package fs

import (
	"log/slog"
	"net/url"
	"time"

	"github.com/spoolersh/pagedog"
)

//go:generate gtrace

// Trace is a set of runtime hooks a [ReportStore] calls as it works.
//
//gtrace:gen
type Trace struct {
	// OnStore is called when a report is being stored, with its incident,
	// and the func it returns when it is, with where it went.
	OnStore func(inc pagedog.Incident) func(*url.URL, error)

	// OnLoad is called when a report is being loaded from u, and the func it
	// returns when it is.
	OnLoad func(u *url.URL) func(error)
}

// LoggerTrace builds a [Trace] that logs every hook to the given logger.
func LoggerTrace(logger *slog.Logger) Trace {
	return Trace{
		OnStore: func(inc pagedog.Incident) func(*url.URL, error) {
			start := time.Now()
			logger := logger.With("key", inc.Key)
			logger.Debug("storing report")
			return func(u *url.URL, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed storing report",
						"error", err,
					)
					return
				}
				logger.Info("stored report",
					"url", u.String(),
				)
			}
		},
		OnLoad: func(u *url.URL) func(error) {
			start := time.Now()
			logger := logger.With("url", u.String())
			logger.Debug("loading report")
			return func(err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed loading report",
						"error", err,
					)
					return
				}
				logger.Debug("loaded report")
			}
		},
	}
}
