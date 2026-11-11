package prometheus

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
		if req.URL.Path != "/api/v1/query_range" {
			http.NotFound(w, req)
			return
		}
		// The client posts the parameters as a form.
		req.ParseForm()
		act = map[string]string{}
		for k := range req.Form {
			act[k] = req.Form.Get(k)
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"__name__":"http_requests_total","code":"500"},"values":[[1794398400,"0"],[1794398430,"3"]]}
		]}}`))
	}))
	defer srv.Close()

	c := &Client{
		URL: srv.URL,
	}
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	series, err := c.QueryRange(context.Background(), `http_requests_total{code="500"}`, start, start.Add(time.Minute), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	expParams := map[string]string{
		"query": `http_requests_total{code="500"}`,
		"start": "1794398400",
		"end":   "1794398460",
		"step":  "30",
	}
	if !cmp.Equal(expParams, act) {
		t.Errorf(
			"params: %s",
			cmp.Diff(expParams, act),
		)
	}
	exp := []Series{
		{
			Metric: map[string]string{
				"__name__": "http_requests_total",
				"code":     "500",
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
	}
	if !cmp.Equal(exp, series) {
		t.Errorf(
			"series: %s",
			cmp.Diff(exp, series),
		)
	}
}

func TestQueryRangeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:5: parse error: unexpected end of input"}`))
	}))
	defer srv.Close()

	c := &Client{
		URL: srv.URL,
	}
	_, err := c.QueryRange(context.Background(), "rate(", time.Now(), time.Now(), time.Second)
	if err == nil {
		t.Fatal("QueryRange(): nil; want an error")
	}
	if exp := "bad_data: invalid parameter"; !strings.Contains(err.Error(), exp) {
		t.Errorf(
			"error: %q; want it to contain %q",
			err, exp,
		)
	}
}

func TestMetricNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/label/__name__/values" {
			http.NotFound(w, req)
			return
		}
		w.Write([]byte(`{"status":"success","data":["http_requests_total","up"]}`))
	}))
	defer srv.Close()

	c := &Client{
		URL: srv.URL,
	}
	act, err := c.MetricNames(context.Background(), time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	exp := []string{"http_requests_total", "up"}
	if !cmp.Equal(exp, act) {
		t.Errorf(
			"names: %s",
			cmp.Diff(exp, act),
		)
	}
}
