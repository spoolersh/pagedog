// Package httputil holds what the http servers and clients of pagedog share.
package httputil

import (
	"net/http"
	"slices"

	"github.com/felixge/httpsnoop"
)

var _ http.RoundTripper = (*HeaderTransport)(nil)

// HeaderTransport is an [http.RoundTripper] that sets Header on every
// request before Base sends it: what an API wants on each call, such as its
// key.
type HeaderTransport struct {
	// Header is set on every request, over the request's own.
	Header http.Header

	// Base sends the request.
	//
	// Nil means [http.DefaultTransport].
	Base http.RoundTripper
}

// RoundTrip implements [http.RoundTripper].
func (t *HeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A round tripper leaves the request it was given alone.
	req = req.Clone(req.Context())
	for name, vs := range t.Header {
		req.Header[name] = slices.Clone(vs)
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

var _ http.Handler = (*TraceHandler)(nil)

// TraceHandler serves every request with Handler and reports it to Trace, with
// the status and the bytes the handler wrote.
type TraceHandler struct {
	Trace   Trace
	Handler http.Handler
}

// ServeHTTP implements [http.Handler].
func (t *TraceHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	done := t.Trace.onRequest(req)
	// httpsnoop wraps res so that the status and the size are seen, and
	// keeps whatever optional interfaces res implements.
	m := httpsnoop.CaptureMetrics(t.Handler, res, req)
	done(m.Code, m.Written)
}
