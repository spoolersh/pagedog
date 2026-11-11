package incidentio

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/pagedog/internal/webhook"
)

// secret is "test-key", as incident.io would show it.
const secret = "whsec_dGVzdC1rZXk="

// sign signs a body the Svix way, under secret, as of the time given.
func sign(id string, at time.Time, body []byte) http.Header {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	timestamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "." + string(body)))
	h := http.Header{}
	h.Set("webhook-id", id)
	h.Set("webhook-timestamp", timestamp)
	h.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return h
}

func TestHandle(t *testing.T) {
	created, err := os.ReadFile("testdata/incident_created.json")
	if err != nil {
		t.Fatal(err)
	}
	// The incident as received is the incident's Raw.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(created, &fields); err != nil {
		t.Fatal(err)
	}
	raw := fields["public_incident.incident_created_v2"]

	now := time.Now()
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
			name:   "makes an incident of a created event",
			secret: secret,
			body:   created,
			header: sign("msg_1", now, created),
			exp: []pagedog.Incident{
				{
					Source:      Name,
					ID:          "01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
					Key:         "incidentio/01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
					Severity:    pagedog.SeverityCritical,
					Title:       "Checkout is failing for every customer",
					Description: "The checkout service answers 500 to every request since 10:00 UTC.",
					StartedAt:   time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
					URL:         "https://app.incident.io/pagedog/incidents/123",
					Raw:         raw,
				},
			},
		},
		{
			name:   "accepts a signature among several",
			secret: secret,
			body:   created,
			header: func() http.Header {
				h := sign("msg_1", now, created)
				h.Set("webhook-signature", "v1,bm90LWl0 "+h.Get("webhook-signature"))
				return h
			}(),
			exp: []pagedog.Incident{
				{
					Source:      Name,
					ID:          "01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
					Key:         "incidentio/01JCK3Y8Q4V3ZJ4M0X7G2P9N6B",
					Severity:    pagedog.SeverityCritical,
					Title:       "Checkout is failing for every customer",
					Description: "The checkout service answers 500 to every request since 10:00 UTC.",
					StartedAt:   time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
					URL:         "https://app.incident.io/pagedog/incidents/123",
					Raw:         raw,
				},
			},
		},
		{
			name:   "makes none of another event",
			secret: secret,
			body:   []byte(`{"event_type":"public_incident.incident_updated_v2"}`),
			header: sign("msg_2", now, []byte(`{"event_type":"public_incident.incident_updated_v2"}`)),
		},
		{
			name:   "makes none of a test incident",
			secret: secret,
			body:   bytes.Replace(created, []byte(`"mode": "standard"`), []byte(`"mode": "test"`), 1),
			header: sign("msg_3", now, bytes.Replace(created, []byte(`"mode": "standard"`), []byte(`"mode": "test"`), 1)),
		},
		{
			name:   "refuses a body signed otherwise",
			secret: secret,
			body:   created,
			header: sign("msg_1", now, []byte(`{}`)),
			expErr: webhook.ErrForbidden,
		},
		{
			name:   "refuses an event without a signature",
			secret: secret,
			body:   created,
			header: http.Header{},
			expErr: webhook.ErrForbidden,
		},
		{
			name:   "refuses a stale event",
			secret: secret,
			body:   created,
			header: sign("msg_1", now.Add(-10*time.Minute), created),
			expErr: webhook.ErrForbidden,
		},
		{
			name:   "refuses everything without a secret",
			body:   created,
			header: sign("msg_1", now, created),
			expErr: webhook.ErrForbidden,
		},
		{
			name:       "fails on a secret that is not a key",
			secret:     "whsec_not base64",
			body:       created,
			header:     sign("msg_1", now, created),
			expErrText: "signing secret",
		},
		{
			name:       "fails on a body that is not an event",
			secret:     secret,
			body:       []byte(`{`),
			header:     sign("msg_1", now, []byte(`{`)),
			expErrText: "decode body",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &Source{
				SigningSecret: tc.secret,
			}
			req := httptest.NewRequest("POST", "/incidentio", bytes.NewReader(tc.body))
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
