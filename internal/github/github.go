// Package github keeps reports as issues of a GitHub repository.
package github

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	google_github "github.com/google/go-github/v93/github"
	"github.com/spoolersh/pagedog"
)

// Label marks the issues this package opens, so that a listing finds them
// and nothing else.
const Label = "pagedog"

var _ pagedog.ReportStore = (*ReportStore)(nil)

// ReportStore keeps each report as an issue titled by its incident, labelled
// [Label] and the severity, with the incident's key hidden in the body. The
// same incident stored again updates the issue opened for it, found by the
// key.
type ReportStore struct {
	Trace Trace

	// Client is the GitHub API client, as [google_github.NewClient] makes it,
	// with a token that may write the repository's issues.
	Client *google_github.Client

	Owner string
	Repo  string
}

// Store opens an issue for the incident, or updates the one opened for it
// before, and returns the issue's URL.
func (s *ReportStore) Store(ctx context.Context, inc pagedog.Incident, r pagedog.Report) (u *url.URL, err error) {
	var updated bool
	done := s.Trace.onStore(inc)
	defer func() { done(u, updated, err) }()

	if inc.Key == "" {
		return nil, errors.New("incident has no key")
	}
	body := r.Text + "\n\n" + marker(inc.Key)

	issue, err := s.find(ctx, inc.Key)
	if err != nil {
		return nil, err
	}
	if issue != nil {
		updated = true
		issue, _, err = s.Client.Issues.Update(ctx,
			s.Owner,
			s.Repo,
			issue.GetNumber(),
			google_github.UpdateIssueRequest{
				Body: google_github.Ptr(body),
			},
		)
	} else {
		issue, _, err = s.Client.Issues.Create(ctx,
			s.Owner,
			s.Repo,
			google_github.CreateIssueRequest{
				Title: cmp.Or(inc.Title, inc.Key),
				Body:  google_github.Ptr(body),
				Labels: []string{
					Label,
					inc.Severity.String(),
				},
			},
		)
	}
	if err != nil {
		return nil, err
	}
	return url.Parse(issue.GetHTMLURL())
}

// Load reads the report back from an issue's URL. A URL of anything but an
// issue of the repository is an error.
func (s *ReportStore) Load(ctx context.Context, u *url.URL) (r pagedog.Report, err error) {
	done := s.Trace.onLoad(u)
	defer func() { done(err) }()

	number, err := s.number(u)
	if err != nil {
		return r, err
	}
	issue, _, err := s.Client.Issues.Get(ctx, s.Owner, s.Repo, number)
	if err != nil {
		return r, err
	}
	body := issue.GetBody()
	i := strings.LastIndex(body, "\n\n"+markerPrefix)
	if i < 0 {
		return r, fmt.Errorf(
			"issue %d carries no report",
			number,
		)
	}
	return pagedog.Report{
		Text: body[:i],
	}, nil
}

// find returns the issue carrying the key, open or closed, or nil when there
// is none.
func (s *ReportStore) find(ctx context.Context, key string) (*google_github.Issue, error) {
	opts := &google_github.IssueListByRepoOptions{
		State: "all",
		Labels: []string{
			Label,
		},
		ListOptions: google_github.ListOptions{
			PerPage: 100,
		},
	}
	for {
		issues, resp, err := s.Client.Issues.ListByRepo(ctx, s.Owner, s.Repo, opts)
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if strings.Contains(issue.GetBody(), marker(key)) {
				return issue, nil
			}
		}
		if resp.NextPage == 0 {
			return nil, nil
		}
		opts.ListOptions.Page = resp.NextPage
	}
}

// number reads the issue number off a URL of the repository.
func (s *ReportStore) number(u *url.URL) (int, error) {
	rest, found := strings.CutPrefix(u.Path, "/"+s.Owner+"/"+s.Repo+"/issues/")
	if !found {
		return 0, fmt.Errorf(
			"not an issue of %s/%s: %s",
			s.Owner, s.Repo, u,
		)
	}
	return strconv.Atoi(rest)
}

const markerPrefix = "<!-- pagedog: "

// marker is the line that carries the key at the end of an issue's body,
// hidden from readers: the issue is found by it, and the report ends before
// it.
func marker(key string) string {
	return markerPrefix + key + " -->"
}
