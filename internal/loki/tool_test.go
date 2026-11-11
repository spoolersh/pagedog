package loki

import (
	"context"
	"testing"
	"time"

	"github.com/spoolersh/pagedog/internal/ai"
)

func TestTools(t *testing.T) {
	src := &stubSource{
		streams: []Stream{
			{
				Labels: map[string]string{
					"container": "demoapp",
				},
				Entries: []Entry{
					{
						Time: time.Date(2026, 11, 11, 12, 0, 30, 0, time.UTC),
						Line: "fail",
					},
				},
			},
		},
		labels: map[string][]string{
			"container": {"demoapp", "pagedog"},
		},
	}
	tools := map[string]ai.Tool{}
	for _, tool := range []ai.Tool{
		&RangeQueryTool{
			Source: src,
		},
		&LabelsTool{
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
			tool:  "query_logs",
			input: `{"query":"{container=\"demoapp\"}","start":"2026-11-11T12:00:00Z","end":"2026-11-11T12:01:00Z","limit":5}`,
			exp: "1 lines in 1 streams from 2026-11-11T12:00:00Z to 2026-11-11T12:01:00Z, newest first\n" +
				"{container=\"demoapp\"}\n" +
				"  2026-11-11T12:00:30Z fail\n",
		},
		{
			tool:  "list_log_labels",
			input: `{}`,
			exp:   "container: demoapp, pagedog\n",
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
	if exp := 5; src.limit != exp {
		t.Errorf(
			"limit: %d; want %d",
			src.limit, exp,
		)
	}
}

type stubSource struct {
	streams []Stream
	labels  map[string][]string

	// limit is what QueryRange was asked.
	limit int
}

func (s *stubSource) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]Stream, error) {
	s.limit = limit
	return s.streams, nil
}

func (s *stubSource) Labels(ctx context.Context, start, end time.Time) ([]string, error) {
	var names []string
	for name := range s.labels {
		names = append(names, name)
	}
	return names, nil
}

func (s *stubSource) LabelValues(ctx context.Context, name string, start, end time.Time) ([]string, error) {
	return s.labels[name], nil
}
