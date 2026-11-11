package pagerduty

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/pagedog/internal/webhook"
)

const secret = "test-secret"

// sign signs a body as PagerDuty does, under secret.
func sign(body []byte) http.Header {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	h := http.Header{}
	h.Set("X-PagerDuty-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	return h
}

func TestHandle(t *testing.T) {
	triggered, err := os.ReadFile("testdata/incident_triggered.json")
	if err != nil {
		t.Fatal(err)
	}
	// The incident as received is the incident's Raw.
	var ev event
	if err := json.Unmarshal(triggered, &ev); err != nil {
		t.Fatal(err)
	}
	raw := []byte(ev.Event.Data)

	exp := []pagedog.Incident{
		{
			Source:    Name,
			ID:        "PGR0VU2",
			Key:       "pagerduty/PGR0VU2",
			Severity:  pagedog.SeverityCritical,
			Title:     "Checkout is failing for every customer",
			StartedAt: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
			URL:       "https://acme.pagerduty.com/incidents/PGR0VU2",
			Raw:       raw,
		},
	}
	for _, tc := range []struct {
		name   string
		secret string
		body   []byte
		header http.Header
		exp    []pagedog.Incident

		// expErr is the error, by errors.Is; expErrText a part of the
		// error's text. Neither means no error.
		expErr     error
		expErrText string
	}{
		{
			name:   "makes an incident of a triggered event",
			secret: secret,
			body:   triggered,
			header: sign(triggered),
			exp:    exp,
		},
		{
			name:   "accepts a signature among several",
			secret: secret,
			body:   triggered,
			header: func() http.Header {
				h := sign(triggered)
				h.Set("X-PagerDuty-Signature", "v1=0a0b,v2=zz,"+h.Get("X-PagerDuty-Signature"))
				return h
			}(),
			exp: exp,
		},
		{
			name:   "makes none of another event",
			secret: secret,
			body:   []byte(`{"event":{"event_type":"pagey.ping"}}`),
			header: sign([]byte(`{"event":{"event_type":"pagey.ping"}}`)),
		},
		{
			name:   "refuses a body signed otherwise",
			secret: secret,
			body:   triggered,
			header: sign([]byte(`{}`)),
			expErr: webhook.ErrForbidden,
		},
		{
			name:   "refuses an event without a signature",
			secret: secret,
			body:   triggered,
			header: http.Header{},
			expErr: webhook.ErrForbidden,
		},
		{
			name:   "refuses everything without a secret",
			body:   triggered,
			header: sign(triggered),
			expErr: webhook.ErrForbidden,
		},
		{
			name:       "fails on a body that is not an event",
			secret:     secret,
			body:       []byte(`{`),
			header:     sign([]byte(`{`)),
			expErrText: "decode body",
		},
		{
			name:       "fails on an incident without an id",
			secret:     secret,
			body:       bytes.Replace(triggered, []byte(`"id": "PGR0VU2"`), []byte(`"id": ""`), 1),
			header:     sign(bytes.Replace(triggered, []byte(`"id": "PGR0VU2"`), []byte(`"id": ""`), 1)),
			expErrText: "incident #2: no id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &Source{
				Secret: tc.secret,
			}
			req := httptest.NewRequest("POST", "/pagerduty", bytes.NewReader(tc.body))
			req.Header = tc.header
			act, err := src.Handle(req)
			switch {
			case tc.expErr != nil:
				if !errors.Is(err, tc.expErr) {
					t.Fatalf(
						"error: %v; want %v",
						err, tc.expErr,
					)
				}
				return
			case tc.expErrText != "":
				if err == nil {
					t.Fatal("Handle(): nil; want an error")
				}
				if act := err.Error(); !strings.Contains(act, tc.expErrText) {
					t.Errorf(
						"error: %q; want it to contain %q",
						act, tc.expErrText,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(tc.exp, act) {
				t.Errorf(
					"incidents: %s",
					cmp.Diff(tc.exp, act),
				)
			}
		})
	}
}
