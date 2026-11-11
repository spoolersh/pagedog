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
				"Authorization": {"Bearer k"},
				"X-Tenant":      {"acme"},
			},
		},
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The transport's header wins over the request's own; the rest of the
	// request's stay, and the request itself is left alone.
	req.Header.Set("X-Tenant", "other")
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	exp := map[string]string{
		"Authorization": "Bearer k",
		"X-Tenant":      "acme",
		"Accept":        "application/json",
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
	if act := req.Header.Get("X-Tenant"); act != "other" {
		t.Errorf(
			"request's header changed: %q",
			act,
		)
	}
}
