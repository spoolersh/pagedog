package pagedog

import (
	"context"
	"errors"
	"testing"
)

func TestSeverity(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		exp  Severity

		// expErr is whether parsing fails; the severity is unknown then.
		expErr bool
	}{
		{
			name: "info",
			text: "info",
			exp:  SeverityInfo,
		},
		{
			name: "warning",
			text: "warning",
			exp:  SeverityWarning,
		},
		{
			name: "critical",
			text: "critical",
			exp:  SeverityCritical,
		},
		{
			name: "any case",
			text: "Critical",
			exp:  SeverityCritical,
		},
		{
			name:   "anything else is unknown",
			text:   "page",
			exp:    SeverityUnknown,
			expErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act, err := ParseSeverity(tc.text)
			if tc.expErr {
				if err == nil {
					t.Fatal("ParseSeverity(): nil; want an error")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if act != tc.exp {
				t.Errorf(
					"severity: %v; want %v",
					act, tc.exp,
				)
			}
			if tc.expErr {
				return
			}
			// The name round-trips through the text form.
			text, err := act.MarshalText()
			if err != nil {
				t.Fatal(err)
			}
			var back Severity
			if err := back.UnmarshalText(text); err != nil {
				t.Fatal(err)
			}
			if back != act {
				t.Errorf(
					"round trip: %v; want %v",
					back, act,
				)
			}
		})
	}
}

func TestNotifiers(t *testing.T) {
	var (
		errFirst = errors.New("first")
		errLast  = errors.New("last")
	)
	for _, tc := range []struct {
		name string

		// errs is what each notifier answers, in order.
		errs []error

		// expErrs are the errors reported, by errors.Is; none means success.
		expErrs []error
	}{
		{
			name: "tells every notifier",
			errs: []error{nil, nil},
		},
		{
			name:    "tells the rest after one fails, and reports every failure",
			errs:    []error{errFirst, nil, errLast},
			expErrs: []error{errFirst, errLast},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				ns    Notifiers
				calls int
			)
			for _, err := range tc.errs {
				ns = append(ns, notifierFunc(func(context.Context, IncidentReport) error {
					calls++
					return err
				}))
			}
			err := ns.Notify(context.Background(), IncidentReport{})
			if act, exp := calls, len(tc.errs); act != exp {
				t.Errorf(
					"notifiers told: %d; want %d",
					act, exp,
				)
			}
			if len(tc.expErrs) == 0 && err != nil {
				t.Fatal(err)
			}
			for _, exp := range tc.expErrs {
				if !errors.Is(err, exp) {
					t.Errorf(
						"error: %v; want it to report %v",
						err, exp,
					)
				}
			}
		})
	}
}

// notifierFunc is a [Notifier] made of a func.
type notifierFunc func(context.Context, IncidentReport) error

func (f notifierFunc) Notify(ctx context.Context, r IncidentReport) error {
	return f(ctx, r)
}
