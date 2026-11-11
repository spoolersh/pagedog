// Package prometheus reads metrics from a Prometheus server through its API
// client.
package prometheus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// Client talks to one Prometheus server.
type Client struct {
	// URL is the server's base URL, such as http://prometheus:9090.
	// Required.
	URL string

	once sync.Once
	api  v1.API
	err  error
}

// Series is one time series a range query returned.
type Series struct {
	Metric map[string]string
	Values []Sample
}

// Sample is one value at one time.
type Sample struct {
	Time time.Time

	// Value is the sample as Prometheus prints it: a float, NaN or Inf.
	Value string
}

func (c *Client) init() (v1.API, error) {
	c.once.Do(func() {
		client, err := api.NewClient(api.Config{
			Address: c.URL,
		})
		if err != nil {
			c.err = err
			return
		}
		c.api = v1.NewAPI(client)
	})
	return c.api, c.err
}

// QueryRange evaluates a PromQL expression at every step from start to end.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Series, error) {
	api, err := c.init()
	if err != nil {
		return nil, err
	}
	value, _, _, err := api.QueryRange(ctx, query, v1.Range{
		Start: start,
		End:   end,
		Step:  step,
	})
	if err != nil {
		return nil, err
	}
	matrix, ok := value.(model.Matrix)
	if !ok {
		return nil, fmt.Errorf(
			"query yields a %s, not a range of series",
			value.Type(),
		)
	}
	series := make([]Series, 0, len(matrix))
	for _, ss := range matrix {
		s := Series{
			Metric: make(map[string]string, len(ss.Metric)),
			Values: make([]Sample, 0, len(ss.Values)),
		}
		for k, v := range ss.Metric {
			s.Metric[string(k)] = string(v)
		}
		for _, v := range ss.Values {
			s.Values = append(s.Values, Sample{
				Time:  v.Timestamp.Time().UTC(),
				Value: v.Value.String(),
			})
		}
		series = append(series, s)
	}
	return series, nil
}

// MetricNames lists the metrics with samples between start and end.
func (c *Client) MetricNames(ctx context.Context, start, end time.Time) ([]string, error) {
	api, err := c.init()
	if err != nil {
		return nil, err
	}
	values, _, _, err := api.LabelValues(ctx, model.MetricNameLabel, nil, start, end)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(values))
	for _, v := range values {
		names = append(names, string(v))
	}
	return names, nil
}
