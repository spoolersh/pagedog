package pagerduty

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

// DefaultAPIURL is PagerDuty's API; accounts in other regions have their
// own, such as https://api.eu.pagerduty.com.
const DefaultAPIURL = "https://api.pagerduty.com"

var _ pagedog.Notifier = (*Notifier)(nil)

// Notifier shares a report on the incident it is about, as a note: the
// report's text when [Notifier.Store] can load it, a link to it otherwise.
// A report on an incident from another source is left alone.
//
// Notes have no idempotency key: a report delivered twice is two notes.
type Notifier struct {
	Trace NotifierTrace

	// APIKey authenticates to the API: a REST API key, or a user's.
	APIKey string

	// From is the email address of a user of the account, which the API
	// wants every note to be from.
	From string

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

// note is the body of POST /incidents/{id}/notes.
// See https://docs.pagerduty.com/developer/api/reference/rest/incidents/create-incident-note.
type note struct {
	Note struct {
		Content string `json:"content"`
	} `json:"note"`
}

// Notify shares the report on its incident, if the incident came from
// PagerDuty.
func (n *Notifier) Notify(ctx context.Context, r pagedog.IncidentReport) (err error) {
	if r.Incident.Source != Name {
		n.Trace.onSkip(r)
		return nil
	}
	done := n.Trace.onNotify(r)
	defer func() { done(err) }()

	content, err := n.content(ctx, r)
	if err != nil {
		return err
	}
	var nt note
	nt.Note.Content = content
	body, err := json.Marshal(nt)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cmp.Or(n.APIURL, DefaultAPIURL)+"/incidents/"+r.Incident.ID+"/notes",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token token="+n.APIKey)
	req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("From", n.From)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		// The API says what was wrong in a short JSON body.
		reason, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
		return fmt.Errorf(
			"pagerduty: %s: %s",
			res.Status, strings.TrimSpace(string(reason)),
		)
	}
	return nil
}

// content is the note, plain text: the report's text, or its URI when a
// reader can follow it, or when there is no store to load it.
func (n *Notifier) content(ctx context.Context, r pagedog.IncidentReport) (string, error) {
	u := r.ReportURI
	switch {
	case u == nil:
		return "", errors.New("no report")
	case u.Scheme == "http" || u.Scheme == "https":
		return "Pagedog's report: " + u.String(), nil
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
