// Package fs keeps reports as files in a directory.
package fs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spoolersh/pagedog"
)

// Ext ends every report's file name.
const Ext = ".md"

var _ pagedog.ReportStore = (*ReportStore)(nil)

// ReportStore keeps each report in a file named by its incident's key, so
// that the same incident stored again overwrites the same file.
type ReportStore struct {
	Trace Trace

	// Dir is the directory the files go in; a key's slashes make
	// subdirectories of it, and a key that would reach outside it is refused.
	//
	// Zero means the working directory.
	Dir string
}

// Store writes the report to Dir/<key>.md, making Dir and the key's
// subdirectories as needed, and returns the file's URL.
func (s *ReportStore) Store(ctx context.Context, inc pagedog.Incident, r pagedog.Report) (u *url.URL, err error) {
	done := s.Trace.onStore(inc)
	defer func() { done(u, err) }()

	if inc.Key == "" {
		return nil, errors.New("incident has no key")
	}
	dir, err := s.dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	name := filepath.FromSlash(inc.Key) + Ext
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return nil, err
	}
	if err := root.WriteFile(name, []byte(r.Text), 0o644); err != nil {
		return nil, err
	}
	return &url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(filepath.Join(dir, name)),
	}, nil
}

// Load reads the report back from a URL Store returned. A URL of another
// scheme, or of a file outside Dir, is an error.
func (s *ReportStore) Load(ctx context.Context, u *url.URL) (r pagedog.Report, err error) {
	done := s.Trace.onLoad(u)
	defer func() { done(err) }()

	if u.Scheme != "file" {
		return r, fmt.Errorf(
			"not a file url: %s",
			u,
		)
	}
	dir, err := s.dir()
	if err != nil {
		return r, err
	}
	name, err := filepath.Rel(dir, filepath.FromSlash(u.Path))
	if err != nil {
		return r, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return r, err
	}
	defer root.Close()

	// The root refuses a name that leads outside it.
	data, err := root.ReadFile(name)
	if err != nil {
		return r, err
	}
	return pagedog.Report{
		Text: string(data),
	}, nil
}

// dir is Dir made absolute, so that file URLs are.
func (s *ReportStore) dir() (string, error) {
	return filepath.Abs(cmp.Or(s.Dir, "."))
}
