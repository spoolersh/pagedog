package ioutil

import (
	"strings"
	"testing"
)

func TestLineColorWriter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		color  Color
		writes []string
		exp    string
	}{
		{
			name:   "one line",
			color:  ColorRed,
			writes: []string{"a\n"},
			exp:    "\x1b[31ma\x1b[0m\n",
		},
		{
			name:   "two lines in one write",
			color:  ColorRed,
			writes: []string{"a\nb\n"},
			exp:    "\x1b[31ma\x1b[0m\n\x1b[31mb\x1b[0m\n",
		},
		{
			name:   "line split across writes",
			color:  ColorRed,
			writes: []string{"a", "b\n"},
			exp:    "\x1b[31mab\x1b[0m\n",
		},
		{
			name:   "write ends midline",
			color:  ColorRed,
			writes: []string{"a\nb"},
			exp:    "\x1b[31ma\x1b[0m\n\x1b[31mb",
		},
		{
			name:   "midline continued then new line",
			color:  ColorRed,
			writes: []string{"a\nb", "c\nd"},
			exp:    "\x1b[31ma\x1b[0m\n\x1b[31mbc\x1b[0m\n\x1b[31md",
		},
		{
			name:   "empty line",
			color:  ColorRed,
			writes: []string{"\n"},
			exp:    "\x1b[31m\x1b[0m\n",
		},
		{
			name:   "empty write",
			color:  ColorRed,
			writes: []string{""},
			exp:    "",
		},
		{
			name:   "another color",
			color:  ColorCyan,
			writes: []string{"a\n"},
			exp:    "\x1b[36ma\x1b[0m\n",
		},
		{
			name:   "zero color passes through",
			writes: []string{"a\nb"},
			exp:    "a\nb",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			w := &LineColorWriter{
				W: &sb,
				C: tc.color,
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
