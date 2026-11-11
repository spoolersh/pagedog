package ioutil

import (
	"bytes"
	"io"
	"strconv"
)

// Color is a text color, as an ANSI SGR code.
type Color uint8

// The basic colors.
const (
	ColorBlack Color = 30 + iota
	ColorRed
	ColorGreen
	ColorYellow
	ColorBlue
	ColorMagenta
	ColorCyan
	ColorWhite
)

// reset is the sequence that ends a color.
const reset = "\x1b[0m"

// appendStart appends the sequence that starts the color to b.
func (c Color) appendStart(b []byte) []byte {
	b = append(b, "\x1b["...)
	b = strconv.AppendUint(b, uint64(c), 10)
	return append(b, 'm')
}

// LineColorWriter is an [io.Writer] that writes every line to W in color C:
// a line starts with the color and is reset before its newline. A write may
// end midline, and the color then stays on until the newline comes.
//
// A zero C writes lines as they are.
//
// It is not safe for concurrent use.
type LineColorWriter struct {
	W io.Writer
	C Color

	midline bool   // The last byte written was not a newline.
	buf     []byte // Reused across writes.
}

// Write writes p to W, each line in color. It returns the number of bytes of
// p written, the color sequences not counted.
func (w *LineColorWriter) Write(p []byte) (n int, err error) {
	if w.C == 0 {
		return w.W.Write(p)
	}
	for len(p) > 0 {
		line := p
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			line = p[:i+1]
		}
		eol := line[len(line)-1] == '\n'

		w.buf = w.buf[:0]
		if !w.midline {
			w.buf = w.C.appendStart(w.buf)
		}
		if eol {
			w.buf = append(w.buf, line[:len(line)-1]...)
			w.buf = append(w.buf, reset...)
			w.buf = append(w.buf, '\n')
		} else {
			w.buf = append(w.buf, line...)
		}
		if _, err := w.W.Write(w.buf); err != nil {
			return n, err
		}
		w.midline = !eol
		n += len(line)
		p = p[len(line):]
	}
	return n, nil
}
