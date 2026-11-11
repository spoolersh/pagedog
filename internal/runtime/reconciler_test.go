package runtime

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"
)

func TestDesired(t *testing.T) {
	for _, tc := range []struct {
		name  string
		queue QueueStats
		min   int
		max   int
		exp   int
	}{
		{
			name: "nothing to do",
			max:  3,
			exp:  0,
		},
		{
			name: "one per visible message",
			queue: QueueStats{
				Visible: 2,
			},
			max: 3,
			exp: 2,
		},
		{
			name: "visible capped at max",
			queue: QueueStats{
				Visible: 10,
			},
			max: 3,
			exp: 3,
		},
		{
			name: "one per pending message",
			queue: QueueStats{
				Pending: 2,
			},
			max: 3,
			exp: 2,
		},
		{
			name: "pending plus visible",
			queue: QueueStats{
				Pending: 1,
				Visible: 1,
			},
			max: 3,
			exp: 2,
		},
		{
			name: "pending plus visible capped at max",
			queue: QueueStats{
				Pending: 2,
				Visible: 2,
			},
			max: 3,
			exp: 3,
		},
		{
			name: "pending alone capped at max",
			queue: QueueStats{
				Pending: 5,
			},
			max: 3,
			exp: 3,
		},
		{
			name: "min workers with nothing to do",
			min:  1,
			max:  3,
			exp:  1,
		},
		{
			name: "min workers below demand",
			queue: QueueStats{
				Visible: 2,
			},
			min: 1,
			max: 3,
			exp: 2,
		},
		{
			name: "min wins over max",
			min:  5,
			max:  3,
			exp:  5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act := desired(tc.queue, tc.min, tc.max)
			if act != tc.exp {
				t.Errorf(
					"desired(%+v, min=%d, max=%d): %d; want %d",
					tc.queue, tc.min, tc.max, act, tc.exp,
				)
			}
		})
	}
}

func TestLoop(t *testing.T) {
	errStop := errors.New("stop")
	for _, tc := range []struct {
		name string

		// stats is what the queue reports per tick, the last repeated; errs
		// the ticks whose queue read fails.
		stats []QueueStats
		errs  []error

		// running is how many workers run before the loop starts.
		running int
		min     int
		max     int
		ticks   int

		expLaunches  int
		expShutdowns int
		expRunning   int
	}{
		{
			name: "launches one worker per message",
			stats: []QueueStats{
				{Visible: 2},
			},
			max:         3,
			ticks:       1,
			expLaunches: 2,
			expRunning:  2,
		},
		{
			name: "launches up to the maximum",
			stats: []QueueStats{
				{Visible: 10},
			},
			max:         3,
			ticks:       1,
			expLaunches: 3,
			expRunning:  3,
		},
		{
			name: "counts the messages being processed",
			stats: []QueueStats{
				{Pending: 1, Visible: 1},
			},
			max:         3,
			ticks:       1,
			expLaunches: 2,
			expRunning:  2,
		},
		{
			name: "shuts down the extra workers",
			stats: []QueueStats{
				{Visible: 1},
			},
			running:      3,
			max:          3,
			ticks:        1,
			expShutdowns: 2,
			expRunning:   1,
		},
		{
			name: "keeps the minimum running",
			stats: []QueueStats{
				{},
			},
			min:         1,
			max:         3,
			ticks:       1,
			expLaunches: 1,
			expRunning:  1,
		},
		{
			name: "goes on after a failed tick",
			stats: []QueueStats{
				{},
				{Visible: 1},
			},
			errs: []error{
				errors.New("queue down"),
			},
			max:         3,
			ticks:       2,
			expLaunches: 1,
			expRunning:  1,
		},
		{
			name: "does nothing before the first tick",
			stats: []QueueStats{
				{Visible: 2},
			},
			max: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(context.Background())
				rt := &stubRuntime{}
				for range tc.running {
					if _, err := rt.Launch(ctx); err != nil {
						t.Fatal(err)
					}
				}
				rt.launches = 0
				r := &Reconciler{
					Queue: &stubQueue{
						stats: tc.stats,
						errs:  tc.errs,
					},
					Runtime:      rt,
					TickInterval: time.Second,
					MinWorkers:   tc.min,
					MaxWorkers:   tc.max,
				}
				done := make(chan error, 1)
				go func() {
					done <- r.Loop(ctx)
				}()
				// Each tick is an interval away; the wait lets it finish.
				for range tc.ticks {
					time.Sleep(time.Second)
					synctest.Wait()
				}
				cancel(errStop)
				if err := <-done; !errors.Is(err, errStop) {
					t.Errorf(
						"loop: %v; want %v",
						err, errStop,
					)
				}
				if act, exp := rt.launches, tc.expLaunches; act != exp {
					t.Errorf(
						"launches: %d; want %d",
						act, exp,
					)
				}
				if act, exp := rt.shutdowns, tc.expShutdowns; act != exp {
					t.Errorf(
						"shutdowns: %d; want %d",
						act, exp,
					)
				}
				if act, exp := len(rt.workers), tc.expRunning; act != exp {
					t.Errorf(
						"running: %d; want %d",
						act, exp,
					)
				}
			})
		})
	}
}

// stubQueue answers each tick from stats, the last one repeated, failing the
// ticks errs names.
type stubQueue struct {
	stats []QueueStats
	errs  []error
	calls int
}

func (q *stubQueue) Stats(context.Context) (QueueStats, error) {
	i := q.calls
	q.calls++
	if i < len(q.errs) && q.errs[i] != nil {
		return QueueStats{}, q.errs[i]
	}
	return q.stats[min(i, len(q.stats)-1)], nil
}

// stubRuntime keeps its workers in a list, launches appending and shutdowns
// removing, and counts both.
type stubRuntime struct {
	seq       int
	workers   []Worker
	launches  int
	shutdowns int
}

func (r *stubRuntime) Stats(context.Context) (Stats, error) {
	return Stats{
		Workers: slices.Clone(r.workers),
	}, nil
}

func (r *stubRuntime) Launch(context.Context) (Worker, error) {
	r.seq++
	w := Worker{
		ID:   strconv.Itoa(r.seq),
		Name: "worker-" + strconv.Itoa(r.seq),
	}
	r.workers = append(r.workers, w)
	r.launches++
	return w, nil
}

func (r *stubRuntime) Shutdown(_ context.Context, w Worker) error {
	r.workers = slices.DeleteFunc(r.workers, func(x Worker) bool {
		return x.ID == w.ID
	})
	r.shutdowns++
	return nil
}
