package webhook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog"
)

func TestHandler(t *testing.T) {
	incidents := []pagedog.Incident{
		{
			Source: "stub",
			ID:     "1",
			Key:    "stub/1",
		},
		{
			Source: "stub",
			ID:     "2",
			Key:    "stub/2",
		},
	}
	for _, tc := range []struct {
		name   string
		method string
		path   string

		// handle is what the source answers; send what the queue does.
		handle    []pagedog.Incident
		handleErr error
		sendErr   error

		expStatus int
		expSent   []pagedog.Incident
	}{
		{
			name:      "sends the incidents a source reads",
			path:      "/stub",
			handle:    incidents,
			expStatus: http.StatusOK,
			expSent:   incidents,
		},
		{
			name:      "answers a source not registered with 404",
			path:      "/nope",
			expStatus: http.StatusNotFound,
		},
		{
			name:      "answers a method other than POST with 405",
			method:    "GET",
			path:      "/stub",
			expStatus: http.StatusMethodNotAllowed,
		},
		{
			name:      "answers a refused notification with 403",
			path:      "/stub",
			handleErr: ErrForbidden,
			expStatus: http.StatusForbidden,
		},
		{
			name:      "answers a notification the source cannot read with 400",
			path:      "/stub",
			handleErr: errors.New("decode body"),
			expStatus: http.StatusBadRequest,
		},
		{
			name:      "answers a queue that refuses with 502",
			path:      "/stub",
			handle:    incidents,
			sendErr:   errors.New("queue down"),
			expStatus: http.StatusBadGateway,
			expSent:   incidents[:1],
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []pagedog.Incident
			s := &Server{
				Queue: &stubQueue{
					DoSend: func(_ context.Context, inc pagedog.Incident) error {
						sent = append(sent, inc)
						return tc.sendErr
					},
				},
			}
			err := s.RegisterSource("stub", &stubSource{
				DoHandle: func(*http.Request) ([]pagedog.Incident, error) {
					return tc.handle, tc.handleErr
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			method := tc.method
			if method == "" {
				method = "POST"
			}
			req := httptest.NewRequest(method, tc.path, strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if act, exp := rec.Code, tc.expStatus; act != exp {
				t.Errorf(
					"status: %d; want %d",
					act, exp,
				)
			}
			if !cmp.Equal(tc.expSent, sent) {
				t.Errorf(
					"sent: %s",
					cmp.Diff(tc.expSent, sent),
				)
			}
		})
	}
}

func TestRegisterSource(t *testing.T) {
	s := &Server{}
	src := &stubSource{}
	if err := s.RegisterSource("stub", src); err != nil {
		t.Fatal(err)
	}
	err := s.RegisterSource("stub", src)
	if err == nil {
		t.Fatal("RegisterSource(): nil; want an error for a name taken")
	}
	if act, exp := err.Error(), "already registered"; !strings.Contains(act, exp) {
		t.Errorf(
			"error: %q; want it to contain %q",
			act, exp,
		)
	}
}

type stubSource struct {
	DoHandle func(*http.Request) ([]pagedog.Incident, error)
}

var _ IncidentSource = (*stubSource)(nil)

func (s *stubSource) Handle(req *http.Request) ([]pagedog.Incident, error) {
	return s.DoHandle(req)
}

type stubQueue struct {
	DoSend func(context.Context, pagedog.Incident) error
}

var _ Queue = (*stubQueue)(nil)

func (q *stubQueue) Send(ctx context.Context, inc pagedog.Incident) error {
	return q.DoSend(ctx, inc)
}
