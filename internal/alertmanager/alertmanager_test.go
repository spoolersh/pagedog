package alertmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spoolersh/pagedog"
	pagedog_webhook "github.com/spoolersh/pagedog/internal/webhook"
)

func TestHandle(t *testing.T) {
	firing := alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname": "HighErrorRate",
			"service":   "checkout",
			"severity":  "critical",
		},
		Annotations: map[string]string{
			"summary":     "checkout 5xx rate above 5%",
			"description": "error rate is 8.1% over the last 5 minutes",
		},
		StartsAt:     time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		GeneratorURL: "http://prometheus:9090/graph?g0.expr=up",
		Fingerprint:  "a1b2c3d4e5f60718",
	}
	resolved := firing
	resolved.Status = "resolved"
	noSummary := firing
	noSummary.Annotations = map[string]string{
		"description": "error rate is 8.1% over the last 5 minutes",
	}
	noGenerator := firing
	noGenerator.GeneratorURL = ""
	oddSeverity := firing
	oddSeverity.Labels = map[string]string{
		"alertname": "HighErrorRate",
		"severity":  "page",
	}
	noFingerprint := firing
	noFingerprint.Fingerprint = ""
	noStart := firing
	noStart.StartsAt = time.Time{}

	// notify is a version 4 notification of the alerts, encoded.
	notify := func(alerts ...alert) []byte {
		body, err := json.Marshal(notification{
			Version:     "4",
			Status:      "firing",
			ExternalURL: externalURL,
			Alerts:      alerts,
		})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	// incident is the incident an alert makes, the alert as sent for its Raw.
	incident := func(a alert, title string, severity pagedog.Severity, url string) pagedog.Incident {
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		return pagedog.Incident{
			Source:      Name,
			ID:          a.Fingerprint,
			Key:         Name + "/" + a.Fingerprint + "/2026-11-09T10:00:00Z",
			Severity:    severity,
			Title:       title,
			Description: a.Annotations["description"],
			StartedAt:   a.StartsAt,
			URL:         url,
			Raw:         raw,
		}
	}
	bearer := func(token string) http.Header {
		return http.Header{
			"Authorization": {"Bearer " + token},
		}
	}

	for _, tc := range []struct {
		name   string
		token  string
		body   []byte
		header http.Header
		exp    []pagedog.Incident

		// expErr is the error, by errors.Is; expErrText a part of the
		// error's text. Neither means no error.
		expErr     error
		expErrText string
	}{
		{
			name:   "makes an incident of a firing alert",
			token:  token,
			body:   notify(firing),
			header: bearer(token),
			exp: []pagedog.Incident{
				incident(firing, "checkout 5xx rate above 5%", pagedog.SeverityCritical, firing.GeneratorURL),
			},
		},
		{
			name:   "makes one per firing alert and skips the resolved",
			token:  token,
			body:   notify(resolved, firing),
			header: bearer(token),
			exp: []pagedog.Incident{
				incident(firing, "checkout 5xx rate above 5%", pagedog.SeverityCritical, firing.GeneratorURL),
			},
		},
		{
			name:   "titles an alert without a summary by its name",
			token:  token,
			body:   notify(noSummary),
			header: bearer(token),
			exp: []pagedog.Incident{
				incident(noSummary, "HighErrorRate", pagedog.SeverityCritical, firing.GeneratorURL),
			},
		},
		{
			name:   "links the Alertmanager when the alert names no generator",
			token:  token,
			body:   notify(noGenerator),
			header: bearer(token),
			exp: []pagedog.Incident{
				incident(noGenerator, "checkout 5xx rate above 5%", pagedog.SeverityCritical, externalURL),
			},
		},
		{
			name:   "takes a severity outside the convention as unknown",
			token:  token,
			body:   notify(oddSeverity),
			header: bearer(token),
			exp: []pagedog.Incident{
				incident(oddSeverity, "checkout 5xx rate above 5%", pagedog.SeverityUnknown, firing.GeneratorURL),
			},
		},
		{
			name:   "refuses a wrong token",
			token:  token,
			body:   notify(firing),
			header: bearer("nope"),
			expErr: pagedog_webhook.ErrForbidden,
		},
		{
			name:   "refuses a notification without a token",
			token:  token,
			body:   notify(firing),
			header: http.Header{},
			expErr: pagedog_webhook.ErrForbidden,
		},
		{
			name:   "refuses everything without a token configured",
			body:   notify(firing),
			header: bearer(token),
			expErr: pagedog_webhook.ErrForbidden,
		},
		{
			name:       "fails on a body that is not a notification",
			token:      token,
			body:       []byte(`{`),
			header:     bearer(token),
			expErrText: "decode body",
		},
		{
			name:       "fails on another version",
			token:      token,
			body:       []byte(`{"version": "3", "alerts": []}`),
			header:     bearer(token),
			expErrText: "unsupported webhook version",
		},
		{
			name:       "fails on an alert without a fingerprint",
			token:      token,
			body:       notify(noFingerprint),
			header:     bearer(token),
			expErrText: "no fingerprint",
		},
		{
			name:       "fails on an alert without a start",
			token:      token,
			body:       notify(noStart),
			header:     bearer(token),
			expErrText: "no startsAt",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/webhook/alertmanager", bytes.NewReader(tc.body))
			req.Header = tc.header
			s := &Source{
				Token: tc.token,
			}
			act, err := s.Handle(req)
			if tc.expErr != nil || tc.expErrText != "" {
				if err == nil {
					t.Fatal("Handle(): nil; want an error")
				}
				if tc.expErr != nil && !errors.Is(err, tc.expErr) {
					t.Errorf(
						"error: %v; want %v",
						err, tc.expErr,
					)
				}
				if tc.expErrText != "" && !strings.Contains(err.Error(), tc.expErrText) {
					t.Errorf(
						"error: %q; want it to contain %q",
						err, tc.expErrText,
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

const (
	token       = "pagedog"
	externalURL = "http://alertmanager:9093"
)

// notification is a webhook notification as the test writes one; the
// package reads it back as its own type, the alerts raw.
type notification struct {
	Version     string  `json:"version"`
	Status      string  `json:"status"`
	ExternalURL string  `json:"externalURL"`
	Alerts      []alert `json:"alerts"`
}
