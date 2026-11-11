package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	google_github "github.com/google/go-github/v93/github"
	"github.com/spoolersh/pagedog"
)

// fakeIssue is an issue as the fake repository keeps it and serves it.
type fakeIssue struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Labels  []string `json:"-"`
	HTMLURL string   `json:"html_url"`
}

// fakeRepo serves the four issue endpoints the store calls, for the
// repository o/r, over the issues it holds.
type fakeRepo struct {
	issues []*fakeIssue
}

func (f *fakeRepo) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/issues", func(w http.ResponseWriter, req *http.Request) {
		json.NewEncoder(w).Encode(f.issues)
	})
	mux.HandleFunc("POST /repos/o/r/issues", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Title  string   `json:"title"`
			Body   string   `json:"body"`
			Labels []string `json:"labels"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		issue := &fakeIssue{
			Number:  len(f.issues) + 1,
			Title:   body.Title,
			Body:    body.Body,
			Labels:  body.Labels,
			HTMLURL: "https://github.com/o/r/issues/" + strconv.Itoa(len(f.issues)+1),
		}
		f.issues = append(f.issues, issue)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(issue)
	})
	mux.HandleFunc("PATCH /repos/o/r/issues/{n}", func(w http.ResponseWriter, req *http.Request) {
		issue := f.issue(req.PathValue("n"))
		if issue == nil {
			http.Error(w, "no such issue", http.StatusNotFound)
			return
		}
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		issue.Body = body.Body
		json.NewEncoder(w).Encode(issue)
	})
	mux.HandleFunc("GET /repos/o/r/issues/{n}", func(w http.ResponseWriter, req *http.Request) {
		issue := f.issue(req.PathValue("n"))
		if issue == nil {
			http.Error(w, "no such issue", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(issue)
	})
	return mux
}

func (f *fakeRepo) issue(number string) *fakeIssue {
	n, err := strconv.Atoi(number)
	if err != nil || n < 1 || n > len(f.issues) {
		return nil
	}
	return f.issues[n-1]
}

func TestReportStore(t *testing.T) {
	repo := &fakeRepo{}
	srv := httptest.NewServer(repo.handler())
	defer srv.Close()

	client, err := google_github.NewClient(
		google_github.WithURLs(google_github.Ptr(srv.URL+"/"), google_github.Ptr(srv.URL+"/")),
	)
	if err != nil {
		t.Fatal(err)
	}
	s := &ReportStore{
		Client: client,
		Owner:  "o",
		Repo:   "r",
	}
	inc := pagedog.Incident{
		Key:      "alertmanager/a1b2/2026-11-09T10:00:00Z",
		Title:    "checkout 5xx rate above 5%",
		Severity: pagedog.SeverityCritical,
	}
	ctx := context.Background()

	u, err := s.Store(ctx, inc, pagedog.Report{
		Text: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if act, exp := u.String(), "https://github.com/o/r/issues/1"; act != exp {
		t.Errorf(
			"url: %s; want %s",
			act, exp,
		)
	}
	expIssue := &fakeIssue{
		Number:  1,
		Title:   "checkout 5xx rate above 5%",
		Body:    "first\n\n<!-- pagedog: alertmanager/a1b2/2026-11-09T10:00:00Z -->",
		Labels:  []string{"pagedog", "critical"},
		HTMLURL: "https://github.com/o/r/issues/1",
	}
	if !cmp.Equal(expIssue, repo.issues[0]) {
		t.Errorf(
			"issue: %s",
			cmp.Diff(expIssue, repo.issues[0]),
		)
	}

	// The same incident again updates the issue, opening none.
	u2, err := s.Store(ctx, inc, pagedog.Report{
		Text: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if act, exp := u2.String(), u.String(); act != exp {
		t.Errorf(
			"url again: %s; want %s",
			act, exp,
		)
	}
	if act, exp := len(repo.issues), 1; act != exp {
		t.Errorf(
			"issues: %d; want %d",
			act, exp,
		)
	}
	r, err := s.Load(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if act, exp := r.Text, "second"; act != exp {
		t.Errorf(
			"loaded: %q; want %q",
			act, exp,
		)
	}

	// Another incident opens another issue.
	u3, err := s.Store(ctx, pagedog.Incident{
		Key: "alertmanager/c3d4/2026-11-09T11:00:00Z",
	}, pagedog.Report{
		Text: "third",
	})
	if err != nil {
		t.Fatal(err)
	}
	if act, exp := u3.String(), "https://github.com/o/r/issues/2"; act != exp {
		t.Errorf(
			"url: %s; want %s",
			act, exp,
		)
	}
	if act, exp := repo.issues[1].Title, "alertmanager/c3d4/2026-11-09T11:00:00Z"; act != exp {
		t.Errorf(
			"untitled incident's issue title: %q; want the key %q",
			act, exp,
		)
	}
}

func TestLoadRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{
			name: "another repository",
			url:  "https://github.com/o/other/issues/1",
		},
		{
			name: "not an issue",
			url:  "https://github.com/o/r/pull/1",
		},
		{
			name: "no number",
			url:  "https://github.com/o/r/issues/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ReportStore{
				Owner: "o",
				Repo:  "r",
			}
			u, err := url.Parse(tc.url)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Load(context.Background(), u)
			if err == nil {
				t.Errorf(
					"Load(): %q, nil; want an error",
					r.Text,
				)
			}
		})
	}
}
