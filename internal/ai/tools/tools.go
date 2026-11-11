// Package tools holds what the agent's tool packages share: the defaults of
// a time range, and how output is printed and bounded.
package tools

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultWindow is how far back a tool looks when the model gives no
	// start.
	DefaultWindow = 15 * time.Minute

	// MaxOutput bounds what a tool returns to the model, in bytes.
	MaxOutput = 16 << 10
)

// Range fills the range the model left open: the end is now, the start is
// window before the end.
func Range(start, end time.Time, window time.Duration) (time.Time, time.Time) {
	if end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		start = end.Add(-window)
	}
	return start.UTC(), end.UTC()
}

// Labels prints a label set the way PromQL and LogQL write a selector.
func Labels(m map[string]string) string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range slices.Sorted(maps.Keys(m)) {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s=%q", k, m[k])
	}
	sb.WriteByte('}')
	return sb.String()
}

// Truncate cuts a tool's output at [MaxOutput], saying so.
func Truncate(s string) string {
	if len(s) <= MaxOutput {
		return s
	}
	return s[:MaxOutput] + "\n... truncated"
}
