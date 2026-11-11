package incidentio

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spoolersh/pagedog"
)

// DefaultAPIURL is incident.io's API.
const DefaultAPIURL = "https://api.incident.io"

var _ pagedog.Notifier = (*Notifier)(nil)

// Notifier shares a report on the incident it is about, as an update: the
// report's text when [Notifier.Store] can load it, a link to it otherwise.
// A report on an incident from another source is left alone.
type Notifier struct {
	Trace NotifierTrace

	// APIKey authenticates to the API, with the incident_updates.create
	// scope.
	APIKey string

	// APIURL is the API's base URL.
	//
	// Zero means [DefaultAPIURL].
	APIURL string

	// Store is optional, to load the text of a report whose URI a reader
	// cannot follow, such as a file's.
	//
	// Zero means such a report goes by its URI alone.
	Store pagedog.ReportStore
}

// update is the body of POST /v2/incident_updates.
// See https://docs.incident.io/api-reference/incident-updates-v2/create.
type update struct {
	IncidentID     string `json:"incident_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Message        string `json:"message"`
}

// Notify shares the report on its incident, if the incident came from
// incident.io.
func (n *Notifier) Notify(ctx context.Context, r pagedog.IncidentReport) (err error) {
	if r.Incident.Source != Name {
		n.Trace.onSkip(r)
		return nil
	}
	done := n.Trace.onNotify(r)
	defer func() { done(err) }()

	message, err := n.message(ctx, r)
	if err != nil {
		return err
	}
	body, err := json.Marshal(update{
		IncidentID: r.Incident.ID,
		// A report may be delivered more than once: under the key, the
		// second update is the first.
		IdempotencyKey: r.Incident.Key,
		Message:        message,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cmp.Or(n.APIURL, DefaultAPIURL)+"/v2/incident_updates",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		// The API says what was wrong in a short JSON body.
		reason, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
		return fmt.Errorf(
			"incident.io: %s: %s",
			res.Status, strings.TrimSpace(string(reason)),
		)
	}
	return nil
}

// message is the update in Markdown: the report's text, or a link to it
// when its URI is one a reader can follow, or its URI when it is not and
// there is no store to load it.
func (n *Notifier) message(ctx context.Context, r pagedog.IncidentReport) (string, error) {
	u := r.ReportURI
	switch {
	case u == nil:
		return "", errors.New("no report")
	case u.Scheme == "http" || u.Scheme == "https":
		return fmt.Sprintf(
			"[Pagedog's report](%s)",
			u,
		), nil
	case n.Store != nil:
		rep, err := n.Store.Load(ctx, u)
		if err != nil {
			return "", err
		}
		return rep.Text, nil
	default:
		return "Pagedog's report: " + u.String(), nil
	}
}
