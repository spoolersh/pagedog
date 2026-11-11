package pagerduty

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

func TestNotify(t *testing.T) {
	inc := pagedog.Incident{
		Source: Name,
		ID:     "PGR0VU2",
		Key:    "pagerduty/PGR0VU2",
		Title:  "Checkout is failing for every customer",
	}
	for _, tc := range []struct {
		name   string
		report pagedog.IncidentReport
		store  pagedog.ReportStore
		status int

		// exp is the note's content posted; empty means none.
		exp string

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
			status: http.StatusOK,
			exp:    "# What happened\n\nThe checkout service ran out of connections.",
		},
		{
			name: "links a report a reader can follow",
			report: pagedog.IncidentReport{
				Incident:  inc,
				ReportURI: &url.URL{Scheme: "https", Host: "github.com", Path: "/o/r/issues/1"},
			},
			status: http.StatusOK,
			exp:    "Pagedog's report: https://github.com/o/r/issues/1",
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
			status: http.StatusOK,
		},
		{
			name: "fails as the api says",
			report: pagedog.IncidentReport{
				Incident:  inc,
				ReportURI: &url.URL{Scheme: "https", Host: "github.com", Path: "/o/r/issues/1"},
			},
			status: http.StatusBadRequest,
			exp:    "Pagedog's report: https://github.com/o/r/issues/1",
			expErr: `400 Bad Request: {"error":{"message":"Invalid Input Provided"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				act    string
				header http.Header
				path   string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
				header = req.Header
				path = req.URL.Path
				body, _ := io.ReadAll(req.Body)
				var nt note
				if err := json.Unmarshal(body, &nt); err != nil {
					t.Errorf("body: %v", err)
				}
				act = nt.Note.Content
				res.WriteHeader(tc.status)
				if tc.status/100 != 2 {
					io.WriteString(res, `{"error":{"message":"Invalid Input Provided"}}`)
				}
			}))
			defer srv.Close()

			n := &Notifier{
				APIKey: "u+test",
				From:   "pagedog@example.com",
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
					"content: %s",
					cmp.Diff(tc.exp, act),
				)
			}
			if act == "" {
				return
			}
			for name, exp := range map[string]string{
				"Authorization": "Token token=u+test",
				"Accept":        "application/vnd.pagerduty+json;version=2",
				"From":          "pagedog@example.com",
			} {
				if act := header.Get(name); act != exp {
					t.Errorf(
						"%s: %q; want %q",
						name, act, exp,
					)
				}
			}
			if exp := "/incidents/PGR0VU2/notes"; path != exp {
				t.Errorf(
					"path: %q; want %q",
					path, exp,
				)
			}
		})
	}
}
