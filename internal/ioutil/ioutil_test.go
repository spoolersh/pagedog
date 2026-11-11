package ioutil

import (
	"io"
	"strings"
	"testing"
)

func TestLinePrefixWriter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		exp    string
	}{
		{
			name:   "one line",
			writes: []string{"a\n"},
			exp:    "> a\n",
		},
		{
			name:   "two lines in one write",
			writes: []string{"a\nb\n"},
			exp:    "> a\n> b\n",
		},
		{
			name:   "line split across writes",
			writes: []string{"a", "b\n"},
			exp:    "> ab\n",
		},
		{
			name:   "write ends midline",
			writes: []string{"a\nb"},
			exp:    "> a\n> b",
		},
		{
			name:   "midline continued then new line",
			writes: []string{"a\nb", "c\nd"},
			exp:    "> a\n> bc\n> d",
		},
		{
			name:   "empty line",
			writes: []string{"\n"},
			exp:    "> \n",
		},
		{
			name:   "no newline at all",
			writes: []string{"abc"},
			exp:    "> abc",
		},
		{
			name:   "empty write",
			writes: []string{""},
			exp:    "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			w := &LinePrefixWriter{
				W: &sb,
				P: "> ",
			}
			writeAll(t, w, tc.writes)
			act := sb.String()
			if act != tc.exp {
				t.Errorf(
					"wrote %q; want %q",
					act, tc.exp,
				)
			}
		})
	}
}

// writeAll writes each string to w, checking that every write is reported
// whole and without error.
func writeAll(t *testing.T, w io.Writer, writes []string) {
	t.Helper()
	for _, s := range writes {
		n, err := w.Write([]byte(s))
		if n != len(s) || err != nil {
			t.Fatalf(
				"Write(%q): %d, %v; want %d, nil",
				s, n, err, len(s),
			)
		}
	}
}
