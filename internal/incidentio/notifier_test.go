package incidentio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog"
)

func TestNotify(t *testing.T) {
	inc := pagedog.Incident{
		Source: Name,
		ID:     "01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
		Key:    "incidentio/01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
		Title:  "Checkout is failing for every customer",
	}
	for _, tc := range []struct {
		name   string
		report pagedog.IncidentReport
		store  pagedog.ReportStore
		status int

		// exp is the update posted; nil means none.
		exp *update

		// expErr is a part of the error's text; empty means no error.
		expErr string
	}{
		{
			name: "shares the report's text",
			report: pagedog.IncidentReport{
				Incident:  inc,
				ReportURI: &url.URL{Scheme: "file", Path: "/reports/x.md"},
			},
			store: &stubStore{
				text: "# What happened\n\nThe checkout service ran out of connections.",
			},
			status: http.StatusCreated,
			exp: &update{
				IncidentID:     "01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
				IdempotencyKey: "incidentio/01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
				Message:        "# What happened\n\nThe checkout service ran out of connections.",
			},
		},
		{
			name: "links a report a reader can follow",
			report: pagedog.IncidentReport{
				Incident:  inc,
				ReportURI: &url.URL{Scheme: "https", Host: "github.com", Path: "/o/r/issues/1"},
			},
			status: http.StatusCreated,
			exp: &update{
				IncidentID:     "01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
				IdempotencyKey: "incidentio/01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
				Message:        "[Pagedog's report](https://github.com/o/r/issues/1)",
			},
		},
		{
			name: "leaves an incident from another source alone",
			report: pagedog.IncidentReport{
				Incident: pagedog.Incident{
					Source: "alertmanager",
					ID:     "a1b2",
					Key:    "alertmanager/a1b2/2026-11-12T10:00:00Z",
				},
				ReportURI: &url.URL{Scheme: "file", Path: "/reports/x.md"},
			},
			status: http.StatusCreated,
		},
		{
			name: "fails as the api says",
			report: pagedog.IncidentReport{
				Incident:  inc,
				ReportURI: &url.URL{Scheme: "https", Host: "github.com", Path: "/o/r/issues/1"},
			},
			status: http.StatusUnprocessableEntity,
			exp: &update{
				IncidentID:     "01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
				IdempotencyKey: "incidentio/01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
				Message:        "[Pagedog's report](https://github.com/o/r/issues/1)",
			},
			expErr: `422 Unprocessable Entity: {"type":"validation_error"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				act  *update
				auth string
				path string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
				auth = req.Header.Get("Authorization")
				path = req.URL.Path
				body, _ := io.ReadAll(req.Body)
				act = new(update)
				if err := json.Unmarshal(body, act); err != nil {
					t.Errorf("body: %v", err)
				}
				res.WriteHeader(tc.status)
				if tc.status/100 != 2 {
					io.WriteString(res, `{"type":"validation_error"}`)
				}
			}))
			defer srv.Close()

			n := &Notifier{
				APIKey: "inc_test",
				APIURL: srv.URL,
				Store:  tc.store,
			}
			err := n.Notify(context.Background(), tc.report)
			if tc.expErr != "" {
				if err == nil {
					t.Fatal("Notify(): nil; want an error")
				}
				if act := err.Error(); !strings.Contains(act, tc.expErr) {
					t.Errorf(
						"error: %q; want it to contain %q",
						act, tc.expErr,
					)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"update: %s",
					cmp.Diff(tc.exp, act),
				)
			}
			if act == nil {
				return
			}
			if exp := "Bearer inc_test"; auth != exp {
				t.Errorf(
					"authorization: %q; want %q",
					auth, exp,
				)
			}
			if exp := "/v2/incident_updates"; path != exp {
				t.Errorf(
					"path: %q; want %q",
					path, exp,
				)
			}
		})
	}
}

type stubStore struct {
	text string
}

var _ pagedog.ReportStore = (*stubStore)(nil)

func (s *stubStore) Store(context.Context, pagedog.Incident, pagedog.Report) (*url.URL, error) {
	return nil, errors.New("stub: Store not implemented")
}

func (s *stubStore) Load(context.Context, *url.URL) (pagedog.Report, error) {
	return pagedog.Report{
		Text: s.text,
	}, nil
}
