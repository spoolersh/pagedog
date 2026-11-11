package webhook

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spoolersh/pagedog"
)

var _ pagedog.Notifier = (*Notifier)(nil)

// Notifier posts one JSON notification per report to a URL: the incident,
// but for its raw payload, and where the report is, with its text when the
// URI is not one a reader can follow and [Notifier.Store] can load it.
type Notifier struct {
	Trace NotifierTrace

	// URL is where the notifications go.
	URL string

	// Token is a bearer token sent with every notification.
	//
	// Zero means none.
	Token string

	// Client sends the requests.
	//
	// Zero means [http.DefaultClient].
	Client *http.Client

	// Store is optional, to load the text of a report whose URI a reader
	// cannot follow, such as a file's.
	//
	// Zero means such a report goes by its URI alone.
	Store pagedog.ReportStore
}

// notification is the body of a notification.
type notification struct {
	Incident incident `json:"incident"`
	Report   report   `json:"report"`
}

type incident struct {
	Source      string           `json:"source"`
	ID          string           `json:"id"`
	Key         string           `json:"key"`
	Severity    pagedog.Severity `json:"severity"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	StartedAt   time.Time        `json:"startedAt"`
	URL         string           `json:"url,omitempty"`
}

type report struct {
	URL  string `json:"url"`
	Text string `json:"text,omitempty"`
}

// Notify posts the report.
func (n *Notifier) Notify(ctx context.Context, r pagedog.IncidentReport) (err error) {
	done := n.Trace.onNotify(r)
	defer func() { done(err) }()

	body, err := n.body(ctx, r)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	res, err := cmp.Or(n.Client, http.DefaultClient).Do(req)
	if err != nil {
		return redact(err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		reason, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
		return fmt.Errorf(
			"webhook: %s: %s",
			res.Status, strings.TrimSpace(string(reason)),
		)
	}
	return nil
}

// body is the notification as JSON.
func (n *Notifier) body(ctx context.Context, r pagedog.IncidentReport) ([]byte, error) {
	inc := r.Incident
	v := notification{
		Incident: incident{
			Source:      inc.Source,
			ID:          inc.ID,
			Key:         inc.Key,
			Severity:    inc.Severity,
			Title:       inc.Title,
			Description: inc.Description,
			StartedAt:   inc.StartedAt,
			URL:         inc.URL,
		},
	}
	u := r.ReportURI
	switch {
	case u == nil:
	case u.Scheme == "http" || u.Scheme == "https" || n.Store == nil:
		v.Report.URL = u.String()
	default:
		rep, err := n.Store.Load(ctx, u)
		if err != nil {
			return nil, err
		}
		v.Report.URL = u.String()
		v.Report.Text = rep.Text
	}
	return json.Marshal(v)
}

// redact strips the URL from an error of the request to it, since
// [url.Error] quotes the URL and it may carry a secret.
func redact(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf(
			"webhook: %s: %w",
			uerr.Op, uerr.Err,
		)
	}
	return err
}
