// Package alertmanager receives Prometheus Alertmanager webhook
// notifications as incidents.
package alertmanager

import (
	"cmp"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spoolersh/pagedog"
	pagedog_webhook "github.com/spoolersh/pagedog/internal/webhook"
)

// Name is the source name incidents from this package carry.
const Name = "alertmanager"

// DefaultMaxBody bounds a notification body.
const DefaultMaxBody = 1 << 20

var _ pagedog_webhook.IncidentSource = (*Source)(nil)

// Source is an [pagedog_webhook.IncidentSource] for Alertmanager's webhook receiver.
//
// Alertmanager must be configured to send a bearer token:
//
//	receivers:
//	  - name: pagedog
//	    webhook_configs:
//	      - url: http://pagedog/alertmanager
//	        http_config:
//	          authorization:
//	            credentials: <token>
type Source struct {
	Trace SourceTrace

	// Token is the bearer token every notification must carry.
	//
	// Zero refuses every notification.
	Token string

	// MaxBody bounds a notification body in bytes.
	//
	// Zero means [DefaultMaxBody].
	MaxBody int64
}

// webhook is Alertmanager's notification, version 4.
// See https://prometheus.io/docs/alerting/latest/configuration/#webhook_config
type webhook struct {
	Version     string `json:"version"`
	GroupKey    string `json:"groupKey"`
	Status      string `json:"status"`
	Receiver    string `json:"receiver"`
	ExternalURL string `json:"externalURL"`

	// Alerts are kept raw: each one is an incident's Raw as well as the
	// input of its parse.
	Alerts []json.RawMessage `json:"alerts"`
}

type alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// Handle parses a notification into one incident per firing alert. A
// resolved alert is not an incident and is skipped. A missing or wrong
// token is [pagedog_webhook.ErrForbidden].
func (s *Source) Handle(req *http.Request) (ret []pagedog.Incident, err error) {
	done := s.Trace.onHandle()
	defer func() { done(len(ret), err) }()

	if !s.authorized(req) {
		return nil, pagedog_webhook.ErrForbidden
	}

	r := http.MaxBytesReader(nil, req.Body, cmp.Or(
		s.MaxBody,
		DefaultMaxBody,
	))
	defer r.Close()

	var w webhook
	dec := json.NewDecoder(r)
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf(
			"decode body: %w",
			err,
		)
	}

	if w.Version != "4" {
		return nil, fmt.Errorf(
			"unsupported webhook version: %q",
			w.Version,
		)
	}
	for i, raw := range w.Alerts {
		var a alert
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf(
				"decode alert %d: %w",
				i, err,
			)
		}
		if a.Status != "firing" {
			s.Trace.onSkipAlert(a.Fingerprint, a.Status)
			continue
		}
		inc, err := a.incident(raw, w.ExternalURL)
		if err != nil {
			return nil, fmt.Errorf(
				"alert %q: %w",
				a.Fingerprint, err,
			)
		}
		ret = append(ret, inc)
	}

	return ret, nil
}

func (s *Source) authorized(req *http.Request) bool {
	if s.Token == "" {
		return false
	}
	scheme, token, _ := strings.Cut(req.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare(
		[]byte(token),
		[]byte(s.Token),
	) == 1
}

// incident maps the alert; raw is the alert as received, externalURL the
// notification's link to the Alertmanager that sent it.
func (a alert) incident(raw []byte, externalURL string) (pagedog.Incident, error) {
	if a.Fingerprint == "" {
		return pagedog.Incident{}, errors.New("no fingerprint")
	}
	if a.StartsAt.IsZero() {
		return pagedog.Incident{}, errors.New("no startsAt")
	}
	// The fingerprint identifies the alert by its labels; startsAt tells
	// one firing of it from the next, so the key does too.
	key := Name + "/" + a.Fingerprint + "/" + a.StartsAt.UTC().Format(time.RFC3339)

	// Alertmanager has no fixed severity: the convention is a "severity"
	// label. Anything outside the convention is unknown, not an error.
	severity, _ := pagedog.ParseSeverity(a.Labels["severity"])

	title := a.Annotations["summary"]
	if title == "" {
		title = a.Labels["alertname"]
	}
	return pagedog.Incident{
		Source:      Name,
		ID:          a.Fingerprint,
		Key:         key,
		Severity:    severity,
		Title:       title,
		Description: a.Annotations["description"],
		StartedAt:   a.StartsAt,
		// The generator is the rule that fired, the closest thing to the
		// alert itself; the Alertmanager that sent it is the fallback.
		URL: cmp.Or(a.GeneratorURL, externalURL),
		Raw: raw,
	}, nil
}
