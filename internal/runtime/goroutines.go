package runtime

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"sync"
)

// ErrShutdown is the cause of a worker's context when [Goroutines.Shutdown]
// or [Goroutines.Close] cancelled it.
var ErrShutdown = errors.New("shut down")

var _ Runtime = (*Goroutines)(nil)

// Goroutines runs workers as goroutines of the process it is in: no engine,
// no image, no network, one process. A worker's panic is recovered on its
// goroutine and reported as the worker's error, stack included, so the
// process goes on as it would after a crashed child. A goroutine the worker
// starts itself is beyond that: a panic there ends the process.
type Goroutines struct {
	Trace GoroutinesTrace

	// Run is the worker body: it is expected to take one message and return.
	//
	// Its context is cancelled by [Goroutines.Shutdown] and
	// [Goroutines.Close], with [ErrShutdown] as the cause.
	Run func(context.Context, Worker) error

	mu      sync.Mutex
	seq     uint64
	workers map[uint64]*goroutine
}

type goroutine struct {
	id     uint64
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// Stats lists the workers still running.
func (g *Goroutines) Stats(context.Context) (Stats, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	stats := Stats{
		Workers: make([]Worker, 0, len(g.workers)),
	}
	for _, w := range g.workers {
		stats.Workers = append(stats.Workers, goroutineWorker(w.id))
	}
	return stats, nil
}

func goroutineWorker(id uint64) Worker {
	str := strconv.FormatUint(id, 10)
	return Worker{
		ID:   str,
		Name: str,
	}
}

// Launch starts one worker. The worker outlives the call, as a container
// would: its context is the runtime's, not ctx.
func (g *Goroutines) Launch(context.Context) (Worker, error) {
	if g.Run == nil {
		return Worker{}, errors.New("goroutines: no run func")
	}
	ctx, cancel := context.WithCancelCause(context.Background())

	g.mu.Lock()
	g.seq++
	w := &goroutine{
		id:     g.seq,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if g.workers == nil {
		g.workers = make(map[uint64]*goroutine)
	}
	g.workers[w.id] = w
	g.mu.Unlock()

	go g.run(ctx, w)

	return goroutineWorker(w.id), nil
}

func (g *Goroutines) run(ctx context.Context, w *goroutine) {
	worker := goroutineWorker(w.id)
	done := g.Trace.onRun(worker)
	var err error
	defer func() {
		// The panic unwound this goroutine alone: the worker's lease is
		// left to expire and the message comes back, as after a crash.
		if v := recover(); v != nil {
			err = fmt.Errorf(
				"panic: %v\n%s",
				v, debug.Stack(),
			)
		}
		g.mu.Lock()
		delete(g.workers, w.id)
		g.mu.Unlock()
		close(w.done)
		done(err)
	}()
	err = g.Run(ctx, worker)
}

// Shutdown cancels the worker and waits for it to return, as long as ctx
// allows. A worker that returned already is not an error.
func (g *Goroutines) Shutdown(ctx context.Context, wr Worker) error {
	id, err := strconv.ParseUint(wr.ID, 10, 64)
	if err != nil {
		return err
	}
	g.mu.Lock()
	w, has := g.workers[id]
	g.mu.Unlock()
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

// Close shuts down every worker still running, as [Goroutines.Shutdown]
// does one, and waits for them to return as long as ctx allows: the workers
// are this process's goroutines, and would be cut off with it otherwise.
func (g *Goroutines) Close(ctx context.Context) error {
	g.mu.Lock()
	workers := make([]*goroutine, 0, len(g.workers))
	for _, w := range g.workers {
		workers = append(workers, w)
	}
	g.mu.Unlock()

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
