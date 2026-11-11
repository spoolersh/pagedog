package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGoroutinesRecoversPanic(t *testing.T) {
	done := make(chan error, 1)
	g := Goroutines{
		Trace: GoroutinesTrace{
			OnRun: func(Worker) func(error) {
				return func(err error) {
					done <- err
				}
			},
		},
		Run: func(context.Context, Worker) error {
			panic("boom")
		},
	}
	if _, err := g.Launch(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if act := err.Error(); !strings.Contains(act, "panic: boom") {
			t.Errorf(
				"error: %q; want it to contain %q",
				act, "panic: boom",
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not return")
	}
	stats, err := g.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if act, exp := len(stats.Workers), 0; act != exp {
		t.Errorf(
			"workers: %d; want %d",
			act, exp,
		)
	}
}

func TestGoroutinesShutdown(t *testing.T) {
	for _, tc := range []struct {
		name string

		// all closes the runtime instead of shutting one worker down.
		all bool
	}{
		{
			name: "shuts one worker down",
		},
		{
			name: "closes every worker",
			all:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu     sync.Mutex
				causes []error
			)
			g := Goroutines{
				Run: func(ctx context.Context, _ Worker) error {
					<-ctx.Done()
					mu.Lock()
					causes = append(causes, context.Cause(ctx))
					mu.Unlock()
					return nil
				},
			}
			ctx := context.Background()
			w, err := g.Launch(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := g.Launch(ctx); err != nil {
				t.Fatal(err)
			}
			exp := 1
			if tc.all {
				err = g.Close(ctx)
				exp = 2
			} else {
				err = g.Shutdown(ctx, w)
			}
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if act := len(causes); act != exp {
				t.Fatalf(
					"workers returned: %d; want %d",
					act, exp,
				)
			}
			for _, cause := range causes {
				if !errors.Is(cause, ErrShutdown) {
					t.Errorf(
						"cause: %v; want %v",
						cause, ErrShutdown,
					)
				}
			}
		})
	}
}
