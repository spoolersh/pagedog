package prometheus

import (
	"context"
	"testing"
	"time"

	"github.com/spoolersh/pagedog/internal/ai"
)

func TestTools(t *testing.T) {
	src := &stubSource{
		series: []Series{
			{
				Metric: map[string]string{
					"code": "500",
				},
				Values: []Sample{
					{
						Time:  time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC),
						Value: "0",
					},
					{
						Time:  time.Date(2026, 11, 11, 12, 0, 30, 0, time.UTC),
						Value: "3",
					},
				},
			},
		},
		names: []string{"http_requests_total", "up"},
	}
	tools := map[string]ai.Tool{}
	for _, tool := range []ai.Tool{
		&RangeQueryTool{
			Source: src,
		},
		&MetricNamesTool{
			Source: src,
		},
	} {
		tools[tool.Spec().Name] = tool
	}
	for _, tc := range []struct {
		tool  string
		input string
		exp   string
	}{
		{
			tool:  "query_metrics",
			input: `{"query":"sum by (code) (rate(http_requests_total[1m]))","start":"2026-11-11T12:00:00Z","end":"2026-11-11T12:01:00Z","step":"1m"}`,
			exp: "1 series from 2026-11-11T12:00:00Z to 2026-11-11T12:01:00Z, step 1m0s\n" +
				"{code=\"500\"}\n" +
				"  2026-11-11T12:00:00Z 0\n" +
				"  2026-11-11T12:00:30Z 3\n",
		},
		{
			tool:  "list_metrics",
			input: `{}`,
			exp:   "http_requests_total\nup",
		},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			act, err := tools[tc.tool].Call(context.Background(), []byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if act != tc.exp {
				t.Errorf(
					"output: %q; want %q",
					act, tc.exp,
				)
			}
		})
	}
	if exp := "sum by (code) (rate(http_requests_total[1m]))"; src.query != exp {
		t.Errorf(
			"query: %q; want %q",
			src.query, exp,
		)
	}
	if exp := time.Minute; src.step != exp {
		t.Errorf(
			"step: %s; want %s",
			src.step, exp,
		)
	}
}

func TestRangeQueryToolDefaults(t *testing.T) {
	src := &stubSource{}
	tool := &RangeQueryTool{
		Source: src,
		Window: time.Hour,
	}
	if _, err := tool.Call(context.Background(), []byte(`{"query":"up"}`)); err != nil {
		t.Fatal(err)
	}
	if act, exp := src.end.Sub(src.start), time.Hour; act != exp {
		t.Errorf(
			"range: %s; want %s",
			act, exp,
		)
	}
	if act, exp := src.step, DefaultStep; act != exp {
		t.Errorf(
			"step: %s; want %s",
			act, exp,
		)
	}
}

type stubSource struct {
	series []Series
	names  []string

	// query is what QueryRange was asked.
	query string
	start time.Time
	end   time.Time
	step  time.Duration
}

func (s *stubSource) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Series, error) {
	s.query, s.start, s.end, s.step = query, start, end, step
	return s.series, nil
}

func (s *stubSource) MetricNames(ctx context.Context, start, end time.Time) ([]string, error) {
	return s.names, nil
}
