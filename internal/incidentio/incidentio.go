// Package incidentio receives incident.io webhook events as incidents, and
// shares the reports on them as updates.
package incidentio

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spoolersh/pagedog"
	"github.com/spoolersh/pagedog/internal/webhook"
)

// Name is the source name incidents from this package carry.
const Name = "incidentio"

const (
	// DefaultMaxBody bounds an event body.
	DefaultMaxBody = 1 << 20

	// DefaultTolerance is how far an event's timestamp may be from now.
	DefaultTolerance = 5 * time.Minute
)

// secretPrefix starts a signing secret as incident.io shows it; the rest is
// the key in base64.
const secretPrefix = "whsec_"

var _ webhook.IncidentSource = (*Source)(nil)

// Source is a [webhook.IncidentSource] for incident.io's webhooks. Every
// incident_created event, public or private, makes an incident, unless the
// incident is a test, a tutorial or a retrospective. Any other event is
// accepted and makes none.
//
// Events are signed the Svix way, with the signing secret of the endpoint
// in incident.io's settings: see https://docs.incident.io/integrations/webhooks.
type Source struct {
	Trace SourceTrace

	// SigningSecret is the endpoint's signing secret, as incident.io shows
	// it: whsec_ and the key in base64.
	//
	// Zero refuses every event.
	SigningSecret string

	// Tolerance is how far an event's timestamp may be from now, either
	// way: a replayed event is refused past it.
	//
	// Zero means [DefaultTolerance].
	Tolerance time.Duration

	// MaxBody bounds an event body in bytes.
	//
	// Zero means [DefaultMaxBody].
	MaxBody int64
}

// incident is what makes a [pagedog.Incident] of an incident_created
// event's incident. See https://docs.incident.io/openapi/webhooks.json.
type incident struct {
	ID        string    `json:"id"`
	Reference string    `json:"reference"`
	Name      string    `json:"name"`
	Summary   string    `json:"summary"`
	Permalink string    `json:"permalink"`
	Mode      string    `json:"mode"`
	CreatedAt time.Time `json:"created_at"`

	// Severity is absent when the incident has none yet.
	Severity *struct {
		Name string `json:"name"`
	} `json:"severity"`
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

	// The event's object sits under the key named after its type.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf(
			"decode body: %w",
			err,
		)
	}
	var eventType string
	if err := json.Unmarshal(fields["event_type"], &eventType); err != nil {
		return nil, fmt.Errorf(
			"decode event_type: %w",
			err,
		)
	}
	switch eventType {
	case "public_incident.incident_created_v2", "private_incident.incident_created_v2":
	default:
		s.Trace.onSkipEvent(eventType)
		return nil, nil
	}
	raw, has := fields[eventType]
	if !has {
		return nil, fmt.Errorf(
			"no %s in the event",
			eventType,
		)
	}
	var inc incident
	if err := json.Unmarshal(raw, &inc); err != nil {
		return nil, fmt.Errorf(
			"decode incident: %w",
			err,
		)
	}
	if inc.Mode != "standard" {
		// A test, a tutorial or a retrospective is not happening now.
		s.Trace.onSkipIncident(inc.Reference, inc.Mode)
		return nil, nil
	}
	i, err := inc.incident(raw)
	if err != nil {
		return nil, fmt.Errorf(
			"incident %s: %w",
			inc.Reference, err,
		)
	}
	return []pagedog.Incident{i}, nil
}

// verify checks an event's signature: HMAC-SHA256 under the key, over the
// id, the timestamp and the body joined by dots, in base64, carried as
// "v1,SIGNATURE" in webhook-signature, several separated by spaces; and the
// timestamp within the tolerance. Anything short of that is
// [webhook.ErrForbidden]. A secret that is not a key is an error of its
// own.
func (s *Source) verify(h http.Header, body []byte) error {
	if s.SigningSecret == "" {
		return webhook.ErrForbidden
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s.SigningSecret, secretPrefix))
	if err != nil {
		return fmt.Errorf(
			"signing secret: %w",
			err,
		)
	}
	var (
		id        = h.Get("webhook-id")
		timestamp = h.Get("webhook-timestamp")
		signature = h.Get("webhook-signature")
	)
	if id == "" || timestamp == "" || signature == "" {
		return webhook.ErrForbidden
	}
	sec, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return webhook.ErrForbidden
	}
	if d := time.Since(time.Unix(sec, 0)).Abs(); d > cmp.Or(s.Tolerance, DefaultTolerance) {
		return webhook.ErrForbidden
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(body)
	exp := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, sig := range strings.Fields(signature) {
		version, value, _ := strings.Cut(sig, ",")
		if version == "v1" && subtle.ConstantTimeCompare([]byte(value), []byte(exp)) == 1 {
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
	// Severities are an organisation's own: one named like pagedog's maps,
	// any other is unknown, not an error.
	var severity pagedog.Severity
	if inc.Severity != nil {
		severity, _ = pagedog.ParseSeverity(inc.Severity.Name)
	}
	return pagedog.Incident{
		Source: Name,
		ID:     inc.ID,
		// An incident is created once: its id is the key.
		Key:         Name + "/" + inc.ID,
		Severity:    severity,
		Title:       inc.Name,
		Description: inc.Summary,
		StartedAt:   inc.CreatedAt,
		URL:         inc.Permalink,
		Raw:         raw,
	}, nil
}
