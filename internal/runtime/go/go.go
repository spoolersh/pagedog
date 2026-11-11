// Package goro runs workers as goroutines of the process it is in, for
// development and tests: no engine, no image, no network.
package goro

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/spoolersh/pagedog/internal/runtime"
)

// ErrShutdown is the cause of a worker's context when [Runtime.Shutdown]
// cancelled it.
var ErrShutdown = errors.New("shut down")

var _ runtime.Runtime = (*Runtime)(nil)

// Runtime launches one goroutine per worker. A panic in a worker is not
// recovered: there is no isolation to pretend to, and a bug should be seen.
type Runtime struct {
	Trace Trace

	// Run is the worker body: it is expected to take one message and return.
	//
	// Its context is cancelled by [Runtime.Shutdown], with [ErrShutdown] as
	// the cause.
	Run func(context.Context) error

	mu      sync.Mutex
	seq     uint64
	workers map[uint64]*worker
}

type worker struct {
	id     uint64
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
		stats.Workers = append(stats.Workers, toWorker(w.id))
	}
	return stats, nil
}

func toWorker(id uint64) runtime.Worker {
	str := strconv.FormatUint(id, 10)
	return runtime.Worker{
		ID:   str,
		Name: str,
	}
}

// Launch starts one worker. The worker outlives the call, as a container
// would: its context is the runtime's, not ctx.
func (r *Runtime) Launch(context.Context) (runtime.Worker, error) {
	if r.Run == nil {
		return runtime.Worker{}, errors.New("goruntime: no run func")
	}
	ctx, cancel := context.WithCancelCause(context.Background())

	r.mu.Lock()
	r.seq++
	id := r.seq
	w := &worker{
		id:     id,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if r.workers == nil {
		r.workers = make(map[uint64]*worker)
	}
	r.workers[id] = w
	r.mu.Unlock()

	go r.run(ctx, w)

	return toWorker(w.id), nil
}

func (r *Runtime) run(ctx context.Context, w *worker) {
	done := r.Trace.onRun(w.id)
	var err error
	defer func() {
		r.mu.Lock()
		delete(r.workers, w.id)
		r.mu.Unlock()
		close(w.done)
		done(err)
	}()
	err = r.Run(ctx)
}

// Shutdown cancels the worker and waits for it to return, as long as ctx
// allows. A worker that returned already is not an error.
func (r *Runtime) Shutdown(ctx context.Context, wr runtime.Worker) error {
	id, err := strconv.ParseUint(wr.ID, 10, 64)
	if err != nil {
		return err
	}
	r.mu.Lock()
	w, has := r.workers[id]
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
