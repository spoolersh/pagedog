// Package stamp carries the binary's build identity, injected at link time
// with -ldflags -X by the release build and the image build; a plain go
// build falls back to the module build info.
package stamp

import (
	"cmp"
	"runtime/debug"
	"sync"
)

// Injected via -ldflags -X; empty under plain go build, where the module
// build info fills revision and built in (version has no build-info
// analog and defaults to "dev").
var (
	version  string
	revision string
	built    string
)

// Version returns the release version the binary was built as.
func Version() string {
	return resolve().version
}

// Revision returns the vcs revision the binary was built from.
func Revision() string {
	return resolve().revision
}

// Built returns the utc timestamp the binary was built at.
func Built() string {
	return resolve().built
}

// stamped is the resolved build identity.
type stamped struct {
	version  string
	revision string
	built    string
}

// resolve prefers the linker-injected values and falls back to the module
// build info (the commit time stands in for the build date there).
var resolve = sync.OnceValue(func() stamped {
	s := stamped{
		version:  cmp.Or(version, "dev"),
		revision: revision,
		built:    built,
	}
	if s.revision != "" && s.built != "" {
		return s
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		s.revision = cmp.Or(s.revision, "unknown")
		s.built = cmp.Or(s.built, "unknown")
		return s
	}
	var (
		rev      string
		modified bool
	)
	for _, x := range info.Settings {
		switch x.Key {
		case "vcs.revision":
			if len(x.Value) >= 12 {
				rev = x.Value[:12]
			}
		case "vcs.time":
			s.built = cmp.Or(s.built, x.Value)
		case "vcs.modified":
			modified = x.Value == "true"
		}
	}
	if modified && rev != "" {
		rev += "-dirty"
	}
	s.revision = cmp.Or(s.revision, rev, "unknown")
	s.built = cmp.Or(s.built, "unknown")
	return s
})
