package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog"
)

func TestNotify(t *testing.T) {
	startedAt := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	inc := pagedog.Incident{
		Source:      "alertmanager",
		ID:          "a1b2",
		Key:         "alertmanager/a1b2/2026-11-09T10:00:00Z",
		Title:       "checkout 5xx rate above 5%",
		Severity:    pagedog.SeverityCritical,
		Description: "error rate is 8.1%",
		StartedAt:   startedAt,
		URL:         "http://prometheus:9090/graph",
		Raw:         []byte(`{"never":"sent"}`),
	}
	expIncident := incident{
		Source:      "alertmanager",
		ID:          "a1b2",
		Key:         "alertmanager/a1b2/2026-11-09T10:00:00Z",
		Severity:    pagedog.SeverityCritical,
		Title:       "checkout 5xx rate above 5%",
		Description: "error rate is 8.1%",
		StartedAt:   startedAt,
		URL:         "http://prometheus:9090/graph",
	}
	for _, tc := range []struct {
		name  string
		uri   string
		token string

		// text is what the store loads for the report; empty means the
		// notifier has no store.
		text string

		// status is the destination's answer; zero means 200.
		status int

		exp       notification
		expAuth   string
		expErr    bool
		expNoBody bool
	}{
		{
			name: "links a web report",
			uri:  "https://github.com/o/r/issues/1",
			exp: notification{
				Incident: expIncident,
				Report: report{
					URL: "https://github.com/o/r/issues/1",
				},
			},
		},
		{
			name: "inlines a file report it can load",
			uri:  "file:///reports/a.md",
			text: "all good",
			exp: notification{
				Incident: expIncident,
				Report: report{
					URL:  "file:///reports/a.md",
					Text: "all good",
				},
			},
		},
		{
			name: "names a file report it cannot load",
			uri:  "file:///reports/a.md",
			exp: notification{
				Incident: expIncident,
				Report: report{
					URL: "file:///reports/a.md",
				},
			},
		},
		{
			name:    "sends its token",
			uri:     "https://github.com/o/r/issues/1",
			token:   "t0k3n",
			expAuth: "Bearer t0k3n",
			exp: notification{
				Incident: expIncident,
				Report: report{
					URL: "https://github.com/o/r/issues/1",
				},
			},
		},
		{
			name:   "fails on a refusal",
			uri:    "https://github.com/o/r/issues/1",
			status: http.StatusBadGateway,
			expErr: true,
			exp: notification{
				Incident: expIncident,
				Report: report{
					URL: "https://github.com/o/r/issues/1",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				act     notification
				actAuth string
				actType string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				actAuth = req.Header.Get("Authorization")
				actType = req.Header.Get("Content-Type")
				if err := json.NewDecoder(req.Body).Decode(&act); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if tc.status != 0 {
					http.Error(w, "down", tc.status)
				}
			}))
			defer srv.Close()

			n := &Notifier{
				URL:   srv.URL,
				Token: tc.token,
			}
			if tc.text != "" {
				n.Store = &stubStore{
					DoLoad: func(context.Context, *url.URL) (pagedog.Report, error) {
						return pagedog.Report{
							Text: tc.text,
						}, nil
					},
				}
			}
			u, err := url.Parse(tc.uri)
			if err != nil {
				t.Fatal(err)
			}
			err = n.Notify(context.Background(), pagedog.IncidentReport{
				Incident:  inc,
				ReportURI: u,
			})
			if (err != nil) != tc.expErr {
				t.Fatalf(
					"Notify(): %v; want error: %t",
					err, tc.expErr,
				)
			}
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"notification: %s",
					cmp.Diff(tc.exp, act),
				)
			}
			if actAuth != tc.expAuth {
				t.Errorf(
					"authorization: %q; want %q",
					actAuth, tc.expAuth,
				)
			}
			if exp := "application/json"; actType != exp {
				t.Errorf(
					"content type: %q; want %q",
					actType, exp,
				)
			}
		})
	}
}

type stubStore struct {
	DoLoad func(context.Context, *url.URL) (pagedog.Report, error)
}

var _ pagedog.ReportStore = (*stubStore)(nil)

func (s *stubStore) Store(context.Context, pagedog.Incident, pagedog.Report) (*url.URL, error) {
	return nil, errors.New("stub: Store not implemented")
}

func (s *stubStore) Load(ctx context.Context, u *url.URL) (pagedog.Report, error) {
	if s.DoLoad != nil {
		return s.DoLoad(ctx, u)
	}
	return pagedog.Report{}, errors.New("stub: Load not implemented")
}
