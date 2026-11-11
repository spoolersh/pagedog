package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "" {
		os.Exit(m.Run())
	}
	switch mode := os.Args[len(os.Args)-1]; mode {
	case "env":
		// The environment, one variable per line, then exit.
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
		os.Exit(0)

	case "wait":
		// Until SIGTERM, whose default action ends the process.
		select {}

	case "ignore":
		// Until killed: SIGTERM is ignored.
		signal.Ignore(syscall.SIGTERM)
		select {}

	default:
		fmt.Fprintln(os.Stderr, "unknown mode:", mode)
		os.Exit(2)
	}
}

func TestLaunchRefusesNoPath(t *testing.T) {
	r := &Runtime{}
	if _, err := r.Launch(context.Background()); err == nil {
		t.Fatal("Launch(): nil; want an error without a path")
	}
}

func TestShutdown(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string

		// mode is what the worker does; stopTimeout how long a shutdown
		// waits for it after SIGTERM.
		mode        string
		stopTimeout time.Duration
	}{
		{
			name: "stops a worker with SIGTERM",
			mode: "wait",
		},
		{
			name:        "kills a worker that ignores SIGTERM after the stop timeout",
			mode:        "ignore",
			stopTimeout: 100 * time.Millisecond,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r := &Runtime{
				Path: exe,
				Args: []string{tc.mode},
				Env: map[string]string{
					childEnv: "1",
				},
				Stdout:      io.Discard,
				Stderr:      io.Discard,
				NoColor:     true,
				StopTimeout: tc.stopTimeout,
			}
			w, err := r.Launch(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if act, exp := w.Name, filepath.Base(exe)+"-1"; act != exp {
				t.Errorf(
					"name: %q; want %q",
					act, exp,
				)
			}
			stats, err := r.Stats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if act, exp := len(stats.Workers), 1; act != exp {
				t.Fatalf(
					"workers: %d; want %d",
					act, exp,
				)
			}
			if act, exp := stats.Workers[0], w; act != exp {
				t.Errorf(
					"worker: %+v; want %+v",
					act, exp,
				)
			}
			if err := r.Shutdown(ctx, w); err != nil {
				t.Fatal(err)
			}
			stats, err = r.Stats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if act, exp := len(stats.Workers), 0; act != exp {
				t.Errorf(
					"workers after shutdown: %d; want %d",
					act, exp,
				)
			}
			// A worker gone already is not an error.
			if err := r.Shutdown(ctx, w); err != nil {
				t.Errorf(
					"second shutdown: %v; want nil",
					err,
				)
			}
		})
	}
}

func TestEnv(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	r := &Runtime{
		Path: exe,
		Args: []string{"env"},
		Env: map[string]string{
			childEnv: "1",
			"FOO":    "bar",
		},
		Stdout:  &out,
		Stderr:  io.Discard,
		NoColor: true,
	}
	w, err := r.Launch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The worker exits on its own; its output is whole once it is gone.
	for {
		stats, err := r.Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(stats.Workers) == 0 {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("the worker did not exit")
		}
	}
	// The environment is Env and nothing else, sorted; every line carries
	// the worker's name.
	exp := w.Name + " | FOO=bar\n" +
		w.Name + " | " + childEnv + "=1\n"
	if act := out.String(); act != exp {
		t.Errorf(
			"output: %q; want %q",
			act, exp,
		)
	}
}

func TestClose(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := &Runtime{
		Path: exe,
		Args: []string{"wait"},
		Env: map[string]string{
			childEnv: "1",
		},
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		NoColor: true,
	}
	for range 2 {
		if _, err := r.Launch(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := r.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if act, exp := len(stats.Workers), 0; act != exp {
		t.Errorf(
			"workers after close: %d; want %d",
			act, exp,
		)
	}
}

// childEnv marks a run of this binary as a worker: [TestMain] then does what
// the last argument says instead of running the tests.
const childEnv = "PAGEDOG_EXEC_TEST_CHILD"
