// Package pagerduty receives PagerDuty webhook events as incidents, and
// shares the reports on them as notes.
package pagerduty

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/pagedog/internal/webhook"
)

// Name is the source name incidents from this package carry.
const Name = "pagerduty"

// DefaultMaxBody bounds an event body.
const DefaultMaxBody = 1 << 20

var _ webhook.IncidentSource = (*Source)(nil)

// Source is a [webhook.IncidentSource] for PagerDuty's v3 webhooks. Every
// incident.triggered event makes an incident; any other event is accepted
// and makes none.
//
// Events are signed under the webhook subscription's secret: see
// https://docs.pagerduty.com/developer/verifying-webhook-signatures.
type Source struct {
	Trace SourceTrace

	// Secret is the webhook subscription's secret, as PagerDuty showed it
	// when the subscription was made.
	//
	// Zero refuses every event.
	Secret string

	// MaxBody bounds an event body in bytes.
	//
	// Zero means [DefaultMaxBody].
	MaxBody int64
}

// event is a webhook payload: one event, whose data is the resource it is
// about. See https://docs.pagerduty.com/developer/webhooks-overview.
type event struct {
	Event struct {
		ID        string          `json:"id"`
		EventType string          `json:"event_type"`
		Data      json.RawMessage `json:"data"`
	} `json:"event"`
}

// incident is what makes a [pagedog.Incident] of an incident.triggered
// event's incident.
type incident struct {
	ID        string    `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	HTMLURL   string    `json:"html_url"`
	Urgency   string    `json:"urgency"`
	CreatedAt time.Time `json:"created_at"`
}

// Handle reads an event into an incident, or none. An event that is not
// signed under the secret is [webhook.ErrForbidden].
func (s *Source) Handle(req *http.Request) (ret []pagedog.Incident, err error) {
	done := s.Trace.onHandle()
	defer func() { done(len(ret), err) }()

	r := http.MaxBytesReader(nil, req.Body, cmp.Or(
		s.MaxBody,
		DefaultMaxBody,
	))
	defer r.Close()

	// The signature is over the body as sent: it is read whole.
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf(
			"read body: %w",
			err,
		)
	}
	if err := s.verify(req.Header, body); err != nil {
		return nil, err
	}
	var ev event
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf(
			"decode body: %w",
			err,
		)
	}
	if ev.Event.EventType != "incident.triggered" {
		s.Trace.onSkipEvent(ev.Event.EventType)
		return nil, nil
	}
	var inc incident
	if err := json.Unmarshal(ev.Event.Data, &inc); err != nil {
		return nil, fmt.Errorf(
			"decode incident: %w",
			err,
		)
	}
	i, err := inc.incident(ev.Event.Data)
	if err != nil {
		return nil, fmt.Errorf(
			"incident #%d: %w",
			inc.Number, err,
		)
	}
	return []pagedog.Incident{i}, nil
}

// verify checks an event's signature: HMAC-SHA256 under the secret, over the
// body, in hex, carried as "v1=SIGNATURE" in X-PagerDuty-Signature, several
// separated by commas while the secret rotates. Anything short of that is
// [webhook.ErrForbidden].
func (s *Source) verify(h http.Header, body []byte) error {
	if s.Secret == "" {
		return webhook.ErrForbidden
	}
	mac := hmac.New(sha256.New, []byte(s.Secret))
	mac.Write(body)
	exp := mac.Sum(nil)
	for _, sig := range strings.Split(h.Get("X-PagerDuty-Signature"), ",") {
		version, value, _ := strings.Cut(sig, "=")
		if version != "v1" {
			continue
		}
		act, err := hex.DecodeString(value)
		if err != nil {
			continue
		}
		if hmac.Equal(act, exp) {
			return nil
		}
	}
	return webhook.ErrForbidden
}

// incident maps the incident; raw is it as received.
func (inc incident) incident(raw []byte) (pagedog.Incident, error) {
	if inc.ID == "" {
		return pagedog.Incident{}, errors.New("no id")
	}
	if inc.CreatedAt.IsZero() {
		return pagedog.Incident{}, errors.New("no created_at")
	}
	// Urgency is PagerDuty's own two levels: high needs someone now, low
	// can wait.
	var severity pagedog.Severity
	switch inc.Urgency {
	case "high":
		severity = pagedog.SeverityCritical
	case "low":
		severity = pagedog.SeverityWarning
	}
	return pagedog.Incident{
		Source: Name,
		ID:     inc.ID,
		// An incident is triggered once: its id is the key.
		Key:       Name + "/" + inc.ID,
		Severity:  severity,
		Title:     inc.Title,
		StartedAt: inc.CreatedAt,
		URL:       inc.HTMLURL,
		Raw:       raw,
	}, nil
}
