package httputil

import (
	"log/slog"
	"net/http"
	"time"
)

//go:generate gtrace

// HandlerTrace is a set of runtime hooks a [Handler] calls as it works.
//
//gtrace:gen
type HandlerTrace struct {
	// OnRequest is called when a request arrives, and the func it returns
	// when it is answered, with the status and the bytes written.
	OnRequest func(*http.Request) func(status int, written int64)
}

// LoggerHandlerTrace builds a [HandlerTrace] that logs every hook to the
// given logger.
func LoggerHandlerTrace(logger *slog.Logger) HandlerTrace {
	return HandlerTrace{
		OnRequest: func(req *http.Request) func(int, int64) {
			start := time.Now()
			logger := logger.With(slog.Group("request",
				"method", req.Method,
				"path", req.URL.Path,
				"remote", req.RemoteAddr,
			))
			logger.Debug("handling request")
			return func(status int, written int64) {
				logger := logger.With(
					"status", status,
					"written", written,
					"latency", time.Since(start),
				)
				switch {
				case status >= 500:
					logger.Error("failed handling request")
				case status >= 400:
					logger.Warn("refused request")
				default:
					logger.Info("handled request")
				}
			}
		},
	}
}
