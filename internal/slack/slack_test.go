package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/spoolersh/pagedog"
)

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

func TestNotify(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		uri   string

		// text is what the store loads for the report; empty means the
		// notifier has no store.
		text string

		// status is Slack's answer; zero means 200.
		status int

		exp    string
		expErr bool
	}{
		{
			name:  "links a web report",
			title: "checkout 5xx rate above 5%",
			uri:   "https://github.com/o/r/issues/1",
			exp:   "*[critical]* checkout 5xx rate above 5%\n<https://github.com/o/r/issues/1|Report>",
		},
		{
			name:  "inlines a file report it can load",
			title: "checkout 5xx rate above 5%",
			uri:   "file:///reports/a.md",
			text:  "all good",
			exp:   "*[critical]* checkout 5xx rate above 5%\n```\nall good\n```",
		},
		{
			name:  "names a file report it cannot load",
			title: "checkout 5xx rate above 5%",
			uri:   "file:///reports/a.md",
			exp:   "*[critical]* checkout 5xx rate above 5%\nReport: file:///reports/a.md",
		},
		{
			name:  "escapes markup",
			title: "a <b> & c",
			uri:   "https://github.com/o/r/issues/1",
			exp:   "*[critical]* a &lt;b&gt; &amp; c\n<https://github.com/o/r/issues/1|Report>",
		},
		{
			name: "titles an untitled incident by its key",
			uri:  "https://github.com/o/r/issues/1",
			exp:  "*[critical]* alertmanager/a1b2/2026-11-09T10:00:00Z\n<https://github.com/o/r/issues/1|Report>",
		},
		{
			name:   "fails on a refusal",
			title:  "checkout 5xx rate above 5%",
			uri:    "https://github.com/o/r/issues/1",
			status: http.StatusNotFound,
			exp:    "*[critical]* checkout 5xx rate above 5%\n<https://github.com/o/r/issues/1|Report>",
			expErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var act string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var m message
				if err := json.NewDecoder(req.Body).Decode(&m); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				act = m.Text
				if tc.status != 0 {
					http.Error(w, "no_service", tc.status)
					return
				}
				w.Write([]byte("ok"))
			}))
			defer srv.Close()

			n := &Notifier{
				URL: srv.URL,
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
				Incident: pagedog.Incident{
					Key:      "alertmanager/a1b2/2026-11-09T10:00:00Z",
					Title:    tc.title,
					Severity: pagedog.SeverityCritical,
				},
				ReportURI: u,
			})
			if (err != nil) != tc.expErr {
				t.Fatalf(
					"Notify(): %v; want error: %t",
					err, tc.expErr,
				)
			}
			if act != tc.exp {
				t.Errorf(
					"text: %q; want %q",
					act, tc.exp,
				)
			}
		})
	}
}

func TestNotifyRedactsURL(t *testing.T) {
	n := &Notifier{
		URL: "http://127.0.0.1:1/services/T000/B000/secret",
	}
	err := n.Notify(context.Background(), pagedog.IncidentReport{})
	if err == nil {
		t.Fatal("Notify(): nil; want an error")
	}
	if act := err.Error(); strings.Contains(act, "secret") {
		t.Errorf(
			"error: %q; want the url redacted",
			act,
		)
	}
}
