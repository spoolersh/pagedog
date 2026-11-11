// Package ioutil holds small io helpers.
package ioutil

import (
	"bytes"
	"io"
)

// LinePrefixWriter is an [io.Writer] that puts P in front of every line it
// writes to W. A line ends with '\n'; a write may end midline, and the next
// one then continues that line without a prefix.
//
// Each line goes to W in a single Write, prefix included, so that lines of
// writers sharing W do not mix within a line.
//
// It is not safe for concurrent use.
type LinePrefixWriter struct {
	W io.Writer
	P string

	midline bool   // The last byte written was not a newline.
	buf     []byte // Reused across writes.
}

// Write writes p to W, a prefix at every line start. It returns the number
// of bytes of p written, the prefixes not counted.
func (w *LinePrefixWriter) Write(p []byte) (n int, err error) {
	for len(p) > 0 {
		line := p
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			line = p[:i+1]
		}
		w.buf = w.buf[:0]
		if !w.midline {
			w.buf = append(w.buf, w.P...)
		}
		w.buf = append(w.buf, line...)
		if _, err := w.W.Write(w.buf); err != nil {
			return n, err
		}
		w.midline = line[len(line)-1] != '\n'
		n += len(line)
		p = p[len(line):]
	}
	return n, nil
}
