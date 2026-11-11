package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestQueryRange(t *testing.T) {
	var act map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/loki/api/v1/query_range" {
			http.NotFound(w, req)
			return
		}
		act = map[string]string{}
		for k := range req.URL.Query() {
			act[k] = req.URL.Query().Get(k)
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
			{"stream":{"container":"demoapp"},"values":[["1794398430000000000","{\"level\":\"INFO\",\"status\":500}"],["1794398400000000000","{\"level\":\"INFO\",\"status\":200}"]]}
		]}}`))
	}))
	defer srv.Close()

	c := &Client{
		URL: srv.URL,
	}
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	streams, err := c.QueryRange(context.Background(), `{container="demoapp"}`, start, start.Add(time.Minute), 50)
	if err != nil {
		t.Fatal(err)
	}
	expParams := map[string]string{
		"query":     `{container="demoapp"}`,
		"start":     "1794398400000000000",
		"end":       "1794398460000000000",
		"limit":     "50",
		"direction": "backward",
	}
	if !cmp.Equal(expParams, act) {
		t.Errorf(
			"params: %s",
			cmp.Diff(expParams, act),
		)
	}
	exp := []Stream{
		{
			Labels: map[string]string{
				"container": "demoapp",
			},
			Entries: []Entry{
				{
					Time: time.Date(2026, 11, 11, 12, 0, 30, 0, time.UTC),
					Line: `{"level":"INFO","status":500}`,
				},
				{
					Time: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC),
					Line: `{"level":"INFO","status":200}`,
				},
			},
		},
	}
	if !cmp.Equal(exp, streams) {
		t.Errorf(
			"streams: %s",
			cmp.Diff(exp, streams),
		)
	}
}

func TestQueryRangeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "parse error at line 1, col 1: syntax error: unexpected IDENTIFIER", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := &Client{
		URL: srv.URL,
	}
	_, err := c.QueryRange(context.Background(), "container", time.Now(), time.Now(), 1)
	if err == nil {
		t.Fatal("QueryRange(): nil; want an error")
	}
	if exp := "400 Bad Request: parse error"; !strings.Contains(err.Error(), exp) {
		t.Errorf(
			"error: %q; want it to contain %q",
			err, exp,
		)
	}
}

func TestLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/loki/api/v1/labels":
			w.Write([]byte(`{"status":"success","data":["container"]}`))
		case "/loki/api/v1/label/container/values":
			w.Write([]byte(`{"status":"success","data":["demoapp","pagedog"]}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	c := &Client{
		URL: srv.URL,
	}
	now := time.Now()
	names, err := c.Labels(context.Background(), now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if exp := []string{"container"}; !cmp.Equal(exp, names) {
		t.Errorf(
			"labels: %s",
			cmp.Diff(exp, names),
		)
	}
	values, err := c.LabelValues(context.Background(), "container", now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if exp := []string{"demoapp", "pagedog"}; !cmp.Equal(exp, values) {
		t.Errorf(
			"values: %s",
			cmp.Diff(exp, values),
		)
	}
}
