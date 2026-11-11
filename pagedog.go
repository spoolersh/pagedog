package pagedog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// Incident is one thing worth investigating, as a source reported it.
type Incident struct {
	// Source names the system the incident came from, such as "alertmanager".
	Source string

	// ID is the source's own identifier of the incident.
	ID string

	// Key identifies the incident across sources and retries: two reports of
	// the same incident carry the same key. It is the deduplication key of the
	// investigation job and the name of its report.
	Key string

	// Title is a one-line statement of what is wrong.
	Title string

	// Severity reports this incident severity.
	Severity Severity

	// Description is a longer statement, when the source has one.
	Description string

	// StartedAt is when the incident began, per the source.
	StartedAt time.Time

	// URL links to the incident at the source, when it has one.
	URL string

	// Raw is the incident as the source sent it, in whatever format the source
	// speaks: a JSON object for most, but not necessarily. It is kept so that
	// nothing the source said is lost to normalization.
	Raw []byte
}

// IncidentReport is what the investigation of an [Incident] came to.
type IncidentReport struct {
	Incident  Incident
	ReportURI *url.URL
}

// Report is what a worker found out about an incident.
type Report struct {
	// Text is the report as the agent wrote it, in Markdown.
	Text string
}

// ReportStore keeps reports where people and other systems find them: an
// issue tracker, an object store, a directory.
type ReportStore interface {
	// Store keeps the report of inc under the incident's key and returns
	// where it is. The incident names and titles the report and is not
	// kept: the queue carries it. The same incident stored again lands in
	// the same place: a redelivered job must not open a second issue.
	Store(ctx context.Context, inc Incident, r Report) (*url.URL, error)

	// Load returns the report Store kept at u. A URL this store did not
	// produce is an error.
	Load(ctx context.Context, u *url.URL) (Report, error)
}

// Notifier tells a destination about a report.
type Notifier interface {
	Notify(ctx context.Context, r IncidentReport) error
}

var _ Notifier = LogNotifier{}

// LogNotifier logs every report it is told about.
type LogNotifier struct {
	Logger *slog.Logger
}

// Notify logs the report's incident and where the report is.
func (n LogNotifier) Notify(ctx context.Context, r IncidentReport) error {
	var u string
	if r.ReportURI != nil {
		u = r.ReportURI.String()
	}
	n.Logger.Info("report",
		slog.Group("incident",
			"source", r.Incident.Source,
			"id", r.Incident.ID,
			"key", r.Incident.Key,
			"severity", r.Incident.Severity,
			"title", r.Incident.Title,
		),
		"url", u,
	)
	return nil
}

// Notifiers tells every notifier in it about a report, and reports every
// failure. A failure nacks the report for all of them: at least once per
// destination.
type Notifiers []Notifier

// Notify tells every notifier about r.
func (ns Notifiers) Notify(ctx context.Context, r IncidentReport) error {
	var errs []error
	for _, n := range ns {
		errs = append(errs, n.Notify(ctx, r))
	}
	return errors.Join(errs...)
}

// Severity is how urgent an incident is.
type Severity uint

const (
	SeverityUnknown Severity = iota
	SeverityInfo
	SeverityWarning
	SeverityCritical
)

// ParseSeverity reads a severity from its name, case-insensitively.
func ParseSeverity(s string) (Severity, error) {
	switch strings.ToLower(s) {
	case "info":
		return SeverityInfo, nil
	case "warning":
		return SeverityWarning, nil
	case "critical":
		return SeverityCritical, nil
	default:
		return SeverityUnknown, fmt.Errorf(
			"unexpected severity: %q",
			s,
		)
	}
}

// String names the severity.
func (s Severity) String() string {
	switch s {
	case SeverityUnknown:
		return "unknown"
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return fmt.Sprintf("Severity(%d)", uint(s))
	}
}

// MarshalText implements [encoding.TextMarshaler] with the severity's name.
func (s Severity) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler] as [ParseSeverity]
// reads a name.
func (s *Severity) UnmarshalText(p []byte) (err error) {
	*s, err = ParseSeverity(string(p))
	return err
}
