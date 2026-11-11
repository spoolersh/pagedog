package github

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
	// and the func it returns when it is, with the issue's URL and whether
	// the issue was there already and got updated.
	OnStore func(inc pagedog.Incident) func(u *url.URL, updated bool, err error)

	// OnLoad is called when a report is being loaded from u, and the func it
	// returns when it is.
	OnLoad func(u *url.URL) func(error)
}

// LoggerTrace builds a [Trace] that logs every hook to the given logger.
func LoggerTrace(logger *slog.Logger) Trace {
	return Trace{
		OnStore: func(inc pagedog.Incident) func(*url.URL, bool, error) {
			start := time.Now()
			logger := logger.With("key", inc.Key)
			logger.Debug("storing report")
			return func(u *url.URL, updated bool, err error) {
				logger := logger.With("latency", time.Since(start))
				if err != nil {
					logger.Error("failed storing report",
						"error", err,
					)
					return
				}
				logger = logger.With("url", u.String())
				if updated {
					logger.Info("updated issue")
					return
				}
				logger.Info("opened issue")
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
