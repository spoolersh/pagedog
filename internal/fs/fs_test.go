package fs

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/spoolersh/pagedog"
)

func TestReportStore(t *testing.T) {
	dir := t.TempDir()
	s := &ReportStore{
		Dir: dir,
	}
	inc := pagedog.Incident{
		Key: "alertmanager/a1b2/2026-11-09T10:00:00Z",
	}
	ctx := context.Background()

	u, err := s.Store(ctx, inc, pagedog.Report{
		Text: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	expPath := filepath.Join(dir, "alertmanager", "a1b2", "2026-11-09T10:00:00Z.md")
	if act, exp := u.String(), "file://"+filepath.ToSlash(expPath); act != exp {
		t.Errorf(
			"url: %s; want %s",
			act, exp,
		)
	}
	if act, err := os.ReadFile(expPath); err != nil || string(act) != "first" {
		t.Errorf(
			"file: %q, %v; want %q, nil",
			act, err, "first",
		)
	}

	// The same incident again lands in the same file.
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
}

func TestStoreRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{
			name: "no key",
			key:  "",
		},
		{
			name: "key leaving the directory",
			key:  "../escape",
		},
		{
			name: "absolute key",
			key:  "/etc/escape",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ReportStore{
				Dir: t.TempDir(),
			}
			inc := pagedog.Incident{
				Key: tc.key,
			}
			u, err := s.Store(context.Background(), inc, pagedog.Report{})
			if err == nil {
				t.Errorf(
					"Store(): %s, nil; want an error",
					u,
				)
			}
		})
	}
}

func TestLoadRefuses(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		url  string
	}{
		{
			name: "another scheme",
			url:  "https://example.com/report.md",
		},
		{
			name: "file outside the directory",
			url:  "file://" + filepath.ToSlash(filepath.Join(filepath.Dir(dir), "escape.md")),
		},
		{
			name: "relative path",
			url:  "file:report.md",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ReportStore{
				Dir: dir,
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
