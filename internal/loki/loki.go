// Package loki reads logs from a Loki server over its HTTP API.
package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one Loki server.
type Client struct {
	// URL is the server's base URL, such as http://loki:3100.
	// Required.
	URL string
}

// Stream is one log stream a query returned: a label set and its lines.
type Stream struct {
	Labels  map[string]string
	Entries []Entry
}

// Entry is one log line.
type Entry struct {
	Time time.Time
	Line string
}

// QueryRange runs a LogQL query from start to end, newest lines first, at
// most limit of them.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]Stream, error) {
	data, err := c.get(ctx, "/loki/api/v1/query_range", url.Values{
		"query":     {query},
		"start":     {strconv.FormatInt(start.UnixNano(), 10)},
		"end":       {strconv.FormatInt(end.UnixNano(), 10)},
		"limit":     {strconv.Itoa(limit)},
		"direction": {"backward"},
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	if res.ResultType != "streams" {
		return nil, fmt.Errorf(
			"query yields %s, not log streams: use a log selector",
			res.ResultType,
		)
	}
	streams := make([]Stream, 0, len(res.Result))
	for _, r := range res.Result {
		s := Stream{
			Labels:  r.Stream,
			Entries: make([]Entry, 0, len(r.Values)),
		}
		for _, pair := range r.Values {
			ns, err := strconv.ParseInt(pair[0], 10, 64)
			if err != nil {
				return nil, err
			}
			s.Entries = append(s.Entries, Entry{
				Time: time.Unix(0, ns).UTC(),
				Line: pair[1],
			})
		}
		streams = append(streams, s)
	}
	return streams, nil
}

// Labels lists the label names of the logs between start and end.
func (c *Client) Labels(ctx context.Context, start, end time.Time) ([]string, error) {
	return c.strings(ctx, "/loki/api/v1/labels", start, end)
}

// LabelValues lists the values a label has between start and end.
func (c *Client) LabelValues(ctx context.Context, name string, start, end time.Time) ([]string, error) {
	return c.strings(ctx, "/loki/api/v1/label/"+url.PathEscape(name)+"/values", start, end)
}

func (c *Client) strings(ctx context.Context, path string, start, end time.Time) ([]string, error) {
	data, err := c.get(ctx, path, url.Values{
		"start": {strconv.FormatInt(start.UnixNano(), 10)},
		"end":   {strconv.FormatInt(end.UnixNano(), 10)},
	})
	if err != nil {
		return nil, err
	}
	var ss []string
	if err := json.Unmarshal(data, &ss); err != nil {
		return nil, err
	}
	return ss, nil
}

// get makes an API request and returns its data, or the error the API
// answered.
func (c *Client) get(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	u := strings.TrimSuffix(c.URL, "/") + path + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		// Loki answers an error as plain text.
		return nil, fmt.Errorf(
			"%s: %s",
			res.Status, strings.TrimSpace(string(body)),
		)
	}
	var answer struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, err
	}
	if answer.Status != "success" {
		return nil, fmt.Errorf(
			"status %q",
			answer.Status,
		)
	}
	return answer.Data, nil
}
