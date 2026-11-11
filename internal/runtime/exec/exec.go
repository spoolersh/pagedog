// Package exec runs workers as processes of the host, one per launch. It is
// the local counterpart of the docker runtime: the same command the image
// would run, without the image.
package exec

import (
	"cmp"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	std_exec "os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spoolersh/pagedog/internal/ioutil"
	"github.com/spoolersh/pagedog/internal/runtime"
)

// DefaultStopTimeout is how long a process has to exit after SIGTERM before
// it is killed.
const DefaultStopTimeout = 10 * time.Second

// ErrShutdown is the cause of a worker's context when [Runtime.Shutdown]
// cancelled it.
var ErrShutdown = errors.New("shut down")

// colors are the colors a worker's output comes in, by the worker's number.
var colors = []ioutil.Color{
	ioutil.ColorGreen,
	ioutil.ColorYellow,
	ioutil.ColorBlue,
	ioutil.ColorMagenta,
	ioutil.ColorCyan,
}

var _ runtime.Runtime = (*Runtime)(nil)

// Runtime launches one process per worker. The program is expected to take
// one message and exit, and to exit on SIGTERM.
type Runtime struct {
	Trace Trace

	// Path is the program to run, as [std_exec.Command] takes it: a path,
	// or a name looked up in PATH.
	// Required.
	Path string

	// Args are the arguments, the program name excluded.
	Args []string

	// Env is the environment every worker gets, and all of it: nothing of
	// this process's is inherited.
	Env map[string]string

	// Dir is the working directory.
	//
	// Zero means this process's.
	Dir string

	// Stdout is where a worker's standard output goes, every line prefixed
	// with the worker's name and in a color by its number.
	//
	// Zero means this process's.
	Stdout io.Writer

	// Stderr is where a worker's standard error goes, decorated as Stdout.
	//
	// Zero means this process's.
	Stderr io.Writer

	// NoColor leaves a worker's output uncolored.
	NoColor bool

	// StopTimeout is how long a shutdown waits for a worker to exit after
	// SIGTERM before killing it.
	//
	// Zero means [DefaultStopTimeout].
	StopTimeout time.Duration

	seq     atomic.Uint64 // Launches so far; it names the workers.
	mu      sync.Mutex
	workers map[int]*worker // By pid.
}

type worker struct {
	name   string
	pid    int
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// Stats lists the workers still running.
func (r *Runtime) Stats(context.Context) (runtime.Stats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stats := runtime.Stats{
		Workers: make([]runtime.Worker, 0, len(r.workers)),
	}
	for _, w := range r.workers {
		stats.Workers = append(stats.Workers, toWorker(w))
	}
	return stats, nil
}

func toWorker(w *worker) runtime.Worker {
	return runtime.Worker{
		ID:   strconv.Itoa(w.pid),
		Name: w.name,
	}
}

// Launch starts one process. The process outlives the call, as a container
// would: its context is the runtime's, not ctx.
func (r *Runtime) Launch(context.Context) (runtime.Worker, error) {
	if r.Path == "" {
		return runtime.Worker{}, errors.New("exec: no path")
	}
	ctx, cancel := context.WithCancelCause(context.Background())

	// The name and the color are chosen here, before the process exists, as
	// they decorate the output from the first byte; the pid comes with the
	// start.
	n := r.seq.Add(1)
	w := &worker{
		name:   filepath.Base(r.Path) + "-" + strconv.FormatUint(n, 10),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	var color ioutil.Color
	if !r.NoColor {
		color = colors[n%uint64(len(colors))]
	}
	decorate := func(out io.Writer) io.Writer {
		return &ioutil.LinePrefixWriter{
			W: &ioutil.LineColorWriter{
				W: out,
				C: color,
			},
			P: w.name + " | ",
		}
	}

	cmd := std_exec.CommandContext(ctx, r.Path, r.Args...)
	// A nil cmd.Env would hand the worker this process's environment: this
	// one is never nil.
	cmd.Env = make([]string, 0, len(r.Env))
	for _, k := range slices.Sorted(maps.Keys(r.Env)) {
		cmd.Env = append(cmd.Env, k+"="+r.Env[k])
	}
	cmd.Dir = r.Dir
	cmd.Stdout = decorate(cmp.Or(r.Stdout, io.Writer(os.Stdout)))
	cmd.Stderr = decorate(cmp.Or(r.Stderr, io.Writer(os.Stderr)))
	// On cancel, ask first; os/exec kills after the delay.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = cmp.Or(r.StopTimeout, DefaultStopTimeout)

	if err := cmd.Start(); err != nil {
		cancel(err)
		return runtime.Worker{}, err
	}
	w.pid = cmd.Process.Pid

	r.mu.Lock()
	if r.workers == nil {
		r.workers = make(map[int]*worker)
	}
	r.workers[w.pid] = w
	r.mu.Unlock()

	go r.wait(cmd, w)

	return toWorker(w), nil
}

func (r *Runtime) wait(cmd *std_exec.Cmd, w *worker) {
	done := r.Trace.onRun(w.pid)
	err := cmd.Wait()

	r.mu.Lock()
	delete(r.workers, w.pid)
	r.mu.Unlock()

	// Release the context's resources; the cause is already set when the
	// exit was a shutdown.
	w.cancel(nil)
	close(w.done)
	done(err)
}

// Shutdown sends SIGTERM to the worker, kills it after [Runtime.StopTimeout]
// if it is still there, and waits for it to exit as long as ctx allows. A
// worker that exited already is not an error.
func (r *Runtime) Shutdown(ctx context.Context, wr runtime.Worker) error {
	pid, err := strconv.Atoi(wr.ID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	w, has := r.workers[pid]
	r.mu.Unlock()
	if !has {
		return nil
	}
	w.cancel(ErrShutdown)
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Close shuts down every worker still running, as [Runtime.Shutdown] does
// one, and waits for them to exit as long as ctx allows. The workers are
// this process's children, and would outlive it otherwise.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	workers := make([]*worker, 0, len(r.workers))
	for _, w := range r.workers {
		workers = append(workers, w)
	}
	r.mu.Unlock()

	for _, w := range workers {
		w.cancel(ErrShutdown)
	}
	for _, w := range workers {
		select {
		case <-w.done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}
