package httputil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler(t *testing.T) {
	for _, tc := range []struct {
		name string

		// status is what the handler sets, zero for none; body what it
		// writes.
		status int
		body   string

		expStatus  int
		expWritten int64
	}{
		{
			name:       "reports the status and the bytes written",
			status:     http.StatusCreated,
			body:       "hello",
			expStatus:  http.StatusCreated,
			expWritten: 5,
		},
		{
			name:       "reports 200 for a handler that writes the body alone",
			body:       "ok",
			expStatus:  http.StatusOK,
			expWritten: 2,
		},
		{
			name:      "reports a status with no body",
			status:    http.StatusNoContent,
			expStatus: http.StatusNoContent,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				actStatus  int
				actWritten int64
				reported   bool
			)
			h := &Handler{
				Trace: HandlerTrace{
					OnRequest: func(*http.Request) func(int, int64) {
						return func(status int, written int64) {
							actStatus, actWritten, reported = status, written, true
						}
					},
				},
				Handler: http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
					if tc.status != 0 {
						res.WriteHeader(tc.status)
					}
					io.WriteString(res, tc.body)
				}),
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			if !reported {
				t.Fatal("the request was not reported")
			}
			if actStatus != tc.expStatus {
				t.Errorf(
					"status: %d; want %d",
					actStatus, tc.expStatus,
				)
			}
			if actWritten != tc.expWritten {
				t.Errorf(
					"written: %d; want %d",
					actWritten, tc.expWritten,
				)
			}
			if act, exp := rec.Code, tc.expStatus; act != exp {
				t.Errorf(
					"response status: %d; want %d",
					act, exp,
				)
			}
		})
	}
}
