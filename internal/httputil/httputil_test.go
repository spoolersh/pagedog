package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestHeaderTransport(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &HeaderTransport{
			Header: http.Header{
				"Signoz-Api-Key": {"k"},
				"X-Signoz-Url":   {"https://acme.signoz.cloud"},
			},
		},
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The transport's header wins over the request's own; the rest of the
	// request's stay, and the request itself is left alone.
	req.Header.Set("X-Signoz-Url", "https://other.signoz.cloud")
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	exp := map[string]string{
		"Signoz-Api-Key": "k",
		"X-Signoz-Url":   "https://acme.signoz.cloud",
		"Accept":         "application/json",
	}
	act := make(map[string]string)
	for name := range exp {
		act[name] = got.Get(name)
	}
	if !cmp.Equal(exp, act) {
		t.Errorf(
			"header: %s",
			cmp.Diff(exp, act),
		)
	}
	if act := req.Header.Get("X-Signoz-Url"); act != "https://other.signoz.cloud" {
		t.Errorf(
			"request's header changed: %q",
			act,
		)
	}
}
