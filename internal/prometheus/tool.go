package prometheus

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spoolersh/pagedog/internal/ai"
	"github.com/spoolersh/pagedog/internal/jsonschema"
)

const (
	// DefaultWindow is how far back a tool looks when the model gives no
	// start.
	DefaultWindow = 15 * time.Minute

	// DefaultStep is the resolution of a range query whose step the model
	// does not give.
	DefaultStep = 30 * time.Second
)

// Source is what the tools read: a [Client] is one.
type Source interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Series, error)
	MetricNames(ctx context.Context, start, end time.Time) ([]string, error)
}

var (
	_ Source  = (*Client)(nil)
	_ ai.Tool = (*RangeQueryTool)(nil)
	_ ai.Tool = (*MetricNamesTool)(nil)
)

// RangeQueryTool is query_metrics: it evaluates a PromQL expression over a
// range and returns every series with its samples.
type RangeQueryTool struct {
	Source Source

	// Window is how far back the tool looks when the model gives no start.
	//
	// Zero means [DefaultWindow].
	Window time.Duration

	// Step is the resolution of a query whose step the model does not give.
	//
	// Zero means [DefaultStep].
	Step time.Duration
}

type rangeQueryInput struct {
	Query string    `json:"query" jsonschema:"the PromQL expression to evaluate"`
	Start time.Time `json:"start,omitempty" jsonschema:"start of the range, RFC 3339; omitted means a while before the end"`
	End   time.Time `json:"end,omitempty" jsonschema:"end of the range, RFC 3339; omitted means now"`
	Step  string    `json:"step,omitempty" jsonschema:"resolution of the range as a duration, such as 30s or 5m"`
}

// Spec describes the tool to the model.
func (t *RangeQueryTool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name: "query_metrics",
		Desc: "Evaluates a PromQL expression over a time range on the Prometheus server " +
			"and returns every series it yields, with the samples at each step.",
		Input: jsonschema.From[rangeQueryInput](),
	}
}

// Call runs the query the model asks.
func (t *RangeQueryTool) Call(ctx context.Context, input []byte) (string, error) {
	var in rangeQueryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	start, end := timeRange(in.Start, in.End, cmp.Or(t.Window, DefaultWindow))
	step := cmp.Or(t.Step, DefaultStep)
	if in.Step != "" {
		var err error
		step, err = time.ParseDuration(in.Step)
		if err != nil {
			return "", fmt.Errorf(
				"step: %w",
				err,
			)
		}
	}
	series, err := t.Source.QueryRange(ctx, in.Query, start, end, step)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d series from %s to %s, step %s\n",
		len(series), start.Format(time.RFC3339), end.Format(time.RFC3339), step,
	)
	for _, s := range series {
		sb.WriteString(labels(s.Metric))
		sb.WriteByte('\n')
		for _, v := range s.Values {
			fmt.Fprintf(&sb, "  %s %s\n", v.Time.Format(time.RFC3339), v.Value)
		}
	}
	return ai.TruncateToolOutput(sb.String()), nil
}

// MetricNamesTool is list_metrics: it names the metrics there are samples
// for in a range.
type MetricNamesTool struct {
	Source Source

	// Window is how far back the tool looks when the model gives no start.
	//
	// Zero means [DefaultWindow].
	Window time.Duration
}

type metricNamesInput struct {
	Start time.Time `json:"start,omitempty" jsonschema:"start of the range, RFC 3339; omitted means a while before the end"`
	End   time.Time `json:"end,omitempty" jsonschema:"end of the range, RFC 3339; omitted means now"`
}

// Spec describes the tool to the model.
func (t *MetricNamesTool) Spec() ai.ToolSpec {
	return ai.ToolSpec{
		Name:  "list_metrics",
		Desc:  "Lists the names of the metrics the Prometheus server has samples for in a time range.",
		Input: jsonschema.From[metricNamesInput](),
	}
}

// Call lists the names for the range the model asks.
func (t *MetricNamesTool) Call(ctx context.Context, input []byte) (string, error) {
	var in metricNamesInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	start, end := timeRange(in.Start, in.End, cmp.Or(t.Window, DefaultWindow))
	names, err := t.Source.MetricNames(ctx, start, end)
	if err != nil {
		return "", err
	}
	return ai.TruncateToolOutput(strings.Join(names, "\n")), nil
}

// timeRange fills the range the model left open: the end is now, the start
// is window before the end.
func timeRange(start, end time.Time, window time.Duration) (time.Time, time.Time) {
	if end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		start = end.Add(-window)
	}
	return start.UTC(), end.UTC()
}

// labels prints a label set the way PromQL writes a selector.
func labels(m map[string]string) string {
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
