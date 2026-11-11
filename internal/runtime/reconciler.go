package runtime

import (
	"cmp"
	"context"
	"time"
)

var (
	DefaultReconcileInterval = time.Second
	DefaultMaxWorkers        = 3
)

// Reconciler keeps one worker per message, those being processed and those
// waiting alike, within a minimum and a maximum: it launches workers when
// there are fewer and shuts them down when there are more. Which of the
// running workers are idle is not known from the outside, so a shutdown
// may hit a busy one: a Runtime must shut down gracefully, and a worker
// cancelled while holding a lease must release the message for redelivery.
type Reconciler struct {
	Trace   ReconcilerTrace
	Queue   Queue
	Runtime Runtime

	// TickInterval is how often the queue and the runtime are compared.
	//
	// Zero means [DefaultReconcileInterval].
	TickInterval time.Duration

	// MinWorkers is how many workers run even with nothing to do.
	//
	// Zero means none.
	MinWorkers int

	// MaxWorkers is how many workers run at most.
	//
	// Zero means [DefaultMaxWorkers].
	MaxWorkers int
}

func (r *Reconciler) interval() time.Duration {
	return cmp.Or(r.TickInterval, DefaultReconcileInterval)
}

func (r *Reconciler) maxWorkers() int {
	return cmp.Or(r.MaxWorkers, DefaultMaxWorkers)
}

// Loop reconciles until ctx is done, waiting the interval between the end
// of one tick and the start of the next, and returns the cause.
func (r *Reconciler) Loop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(r.interval()):
			r.tick(ctx)
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	qstats, err := r.queueStats(ctx)
	if err != nil {
		return
	}
	rstats, err := r.runtimeStats(ctx)
	if err != nil {
		return
	}
	var (
		running = len(rstats.Workers)
		want    = desired(qstats, r.MinWorkers, r.maxWorkers())
	)
	switch {
	case running > want:
		// More workers than messages: some are idle, or the limit shrank.
		// The listing does not say which are idle, so the last listed go.
		for _, w := range rstats.Workers[want:] {
			if err := r.shutdown(ctx, w); err != nil {
				return
			}
		}
	case running < want:
		for range want - running {
			if _, err := r.launch(ctx); err != nil {
				return
			}
		}
	}
}

// desired is how many workers the queue wants running, within the bounds:
// one per message being processed, plus one per message waiting.
//
// Each worker holds one lease at most, and nothing but workers lease, so
// pending counts the busy workers, a dead worker's lease included until it
// expires: a replacement for it is wanted already. Visible counts the
// messages no worker holds, each wanting a worker of its own.
//
// MinWorkers wins over maxWorkers when they disagree.
func desired(q QueueStats, minWorkers, maxWorkers int) int {
	want := int(q.Pending + q.Visible)
	want = min(want, maxWorkers)
	want = max(want, minWorkers)
	return want
}

func (r *Reconciler) queueStats(ctx context.Context) (stats QueueStats, err error) {
	done := r.Trace.onQueueStats()
	defer func() { done(stats, err) }()
	return r.Queue.Stats(ctx)
}

func (r *Reconciler) runtimeStats(ctx context.Context) (stats Stats, err error) {
	done := r.Trace.onRuntimeStats()
	defer func() { done(stats, err) }()
	return r.Runtime.Stats(ctx)
}

func (r *Reconciler) launch(ctx context.Context) (w Worker, err error) {
	done := r.Trace.onLaunch()
	defer func() { done(w, err) }()
	return r.Runtime.Launch(ctx)
}

func (r *Reconciler) shutdown(ctx context.Context, w Worker) (err error) {
	done := r.Trace.onShutdown(w)
	defer func() { done(err) }()
	return r.Runtime.Shutdown(ctx, w)
}
