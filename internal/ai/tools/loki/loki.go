// Package loki gives an agent tools over a Loki server.
package loki

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spoolersh/pagedog/internal/ai"
	"github.com/spoolersh/pagedog/internal/ai/tools"
	"github.com/spoolersh/pagedog/internal/jsonschema"
	"github.com/spoolersh/pagedog/internal/loki"
)

// DefaultLimit is how many log lines a query returns at most when the model
// gives no limit.
const DefaultLimit = 100

// Source is what the tools read: a [loki.Client] is one.
type Source interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]loki.Stream, error)
	Labels(ctx context.Context, start, end time.Time) ([]string, error)
	LabelValues(ctx context.Context, name string, start, end time.Time) ([]string, error)
}

var (
	_ Source  = (*loki.Client)(nil)
	_ ai.Tool = (*RangeQueryTool)(nil)
	_ ai.Tool = (*LabelsTool)(nil)
)

// RangeQueryTool is query_logs: it runs a LogQL query over a range and
// returns the matching lines, newest first, grouped by stream.
type RangeQueryTool struct {
	Source Source

	// Window is how far back the tool looks when the model gives no start.
	//
	// Zero means [tools.DefaultWindow].
	Window time.Duration

	// Limit is how many lines a query returns at most when the model gives
	// no limit.
	//
	// Zero means [DefaultLimit].
	Limit int
}

type rangeQueryInput struct {
	Query string    `json:"query" jsonschema:"the LogQL query, with a stream selector"`
	Start time.Time `json:"start,omitempty" jsonschema:"start of the range, RFC 3339; omitted means a while before the end"`
	End   time.Time `json:"end,omitempty" jsonschema:"end of the range, RFC 3339; omitted means now"`
	Limit int       `json:"limit,omitempty" jsonschema:"most lines to return"`
}

// Spec describes the tool to the model.
func (t *RangeQueryTool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name: "query_logs",
		Desc: "Runs a LogQL query over a time range on the Loki server and returns the " +
			"matching log lines, newest first, grouped by stream. The query needs a " +
			"stream selector, such as {container=\"demoapp\"} |= \"error\".",
		Input: jsonschema.From[rangeQueryInput](),
	}
}

// Call runs the query the model asks.
func (t *RangeQueryTool) Call(ctx context.Context, input []byte) (string, error) {
	var in rangeQueryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	start, end := tools.Range(in.Start, in.End, cmp.Or(t.Window, tools.DefaultWindow))
	limit := cmp.Or(in.Limit, t.Limit, DefaultLimit)
	streams, err := t.Source.QueryRange(ctx, in.Query, start, end, limit)
	if err != nil {
		return "", err
	}
	var lines int
	for _, s := range streams {
		lines += len(s.Entries)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d lines in %d streams from %s to %s, newest first\n",
		lines, len(streams), start.Format(time.RFC3339), end.Format(time.RFC3339),
	)
	for _, s := range streams {
		sb.WriteString(tools.Labels(s.Labels))
		sb.WriteByte('\n')
		for _, e := range s.Entries {
			fmt.Fprintf(&sb, "  %s %s\n", e.Time.Format(time.RFC3339Nano), e.Line)
		}
	}
	return tools.Truncate(sb.String()), nil
}

// LabelsTool is list_log_labels: it names the labels the logs carry in a
// range, with the values each has.
type LabelsTool struct {
	Source Source

	// Window is how far back the tool looks when the model gives no start.
	//
	// Zero means [tools.DefaultWindow].
	Window time.Duration
}

type labelsInput struct {
	Start time.Time `json:"start,omitempty" jsonschema:"start of the range, RFC 3339; omitted means a while before the end"`
	End   time.Time `json:"end,omitempty" jsonschema:"end of the range, RFC 3339; omitted means now"`
}

// Spec describes the tool to the model.
func (t *LabelsTool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name:  "list_log_labels",
		Desc:  "Lists the labels the Loki server's logs carry in a time range, with the values each has.",
		Input: jsonschema.From[labelsInput](),
	}
}

// Call lists the labels for the range the model asks.
func (t *LabelsTool) Call(ctx context.Context, input []byte) (string, error) {
	var in labelsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	start, end := tools.Range(in.Start, in.End, cmp.Or(t.Window, tools.DefaultWindow))
	names, err := t.Source.Labels(ctx, start, end)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, name := range names {
		values, err := t.Source.LabelValues(ctx, name, start, end)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "%s: %s\n", name, strings.Join(values, ", "))
	}
	return tools.Truncate(sb.String()), nil
}
