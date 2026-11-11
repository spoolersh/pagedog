// Package slack tells a Slack channel about reports, through an incoming
// webhook.
package slack

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

	"github.com/spoolersh/pagedog"
)

var _ pagedog.Notifier = (*Notifier)(nil)

// Notifier posts one message per report to an incoming webhook: the incident's
// severity and title, then the report as a link when its URI is one a reader
// can follow, or its text when it is not and [Notifier.Store] can load it.
type Notifier struct {
	Trace NotifierTrace

	// URL is the incoming webhook URL. It carries its own secret, so it never
	// goes into a log or an error.
	URL string

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

// message is a webhook payload.
type message struct {
	Text string `json:"text"`
}

// Notify posts the report.
func (n *Notifier) Notify(ctx context.Context, r pagedog.IncidentReport) (err error) {
	done := n.Trace.onNotify(r)
	defer func() { done(err) }()

	text, err := n.text(ctx, r)
	if err != nil {
		return err
	}
	body, err := json.Marshal(message{
		Text: text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := cmp.Or(n.Client, http.DefaultClient).Do(req)
	if err != nil {
		return redact(err)
	}
	defer res.Body.Close()

	if res.StatusCode/100 != 2 {
		// Slack says what was wrong in a short body.
		reason, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
		return fmt.Errorf(
			"slack: %s: %s",
			res.Status, strings.TrimSpace(string(reason)),
		)
	}
	return nil
}

// text is the message in Slack's markup: the severity and title, then the
// report as a link, or as text when the URI is not one a reader can follow
// and the store is there to load it, or by its URI when it is not.
func (n *Notifier) text(ctx context.Context, r pagedog.IncidentReport) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb,
		"*[%s]* %s",
		r.Incident.Severity, escape(cmp.Or(r.Incident.Title, r.Incident.Key)),
	)
	u := r.ReportURI
	switch {
	case u == nil:
	case u.Scheme == "http" || u.Scheme == "https":
		fmt.Fprintf(&sb,
			"\n<%s|Report>",
			u,
		)
	case n.Store != nil:
		rep, err := n.Store.Load(ctx, u)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb,
			"\n```\n%s\n```",
			escape(rep.Text),
		)
	default:
		fmt.Fprintf(&sb,
			"\nReport: %s",
			escape(u.String()),
		)
	}
	return sb.String(), nil
}

// escape makes text safe for Slack's markup, which reads & < > as its own.
func escape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(s)
}

// redact strips the webhook URL from an error of the request to it, since
// [url.Error] quotes the URL.
func redact(err error) error {
	if e, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf(
			"slack: %s: %w",
			e.Op, e.Err,
		)
	}
	return err
}
