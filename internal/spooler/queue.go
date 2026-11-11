// Package spooler sends messages to Spooler queues and takes them off: a
// [Queue] for one queue, a [Pipe] for moving messages from one queue to
// another.
package spooler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/spoolersh/pagedog/internal/runtime"
	"github.com/spoolersh/spooler-go"
)

// Queue is one Spooler queue, with the messages on it as T.
type Queue[T any] struct {
	Client *spooler.Client
	Spool  string
	Queue  string

	// Decode turns a message into a T.
	//
	// Zero means JSON.
	Decode func([]byte) (T, error)

	// Encode turns a T into a message.
	//
	// Zero means JSON.
	Encode func(T) ([]byte, error)

	// Dedup names a message for deduplication: the name is hashed into the
	// dedup key the message is sent with. An empty name means no dedup for
	// that message.
	//
	// Zero means no dedup.
	Dedup func(T) string

	// ShutdownTimeout is how long [Queue.Drain] keeps talking to the server
	// after its context ends, so that a receive in flight completes and its
	// message is released, and a settlement in progress completes.
	//
	// Zero means [DefaultShutdownTimeout].
	ShutdownTimeout time.Duration
}

// Stats reads the queue's message counts.
func (q *Queue[T]) Stats(ctx context.Context) (_ runtime.QueueStats, err error) {
	queue, err := q.Client.Queue(ctx, spooler.QueueRequest{
		Spool: q.Spool,
		Queue: q.Queue,
	})
	if err != nil {
		return
	}
	return runtime.QueueStats{
		Visible: queue.Stats.Visible,
		Pending: queue.Stats.Pending,
	}, nil
}

// Send puts v on the queue, deduplicated by [Queue.Dedup] when set.
func (q *Queue[T]) Send(ctx context.Context, v T) error {
	data, err := encode(q.Encode, v)
	if err != nil {
		return err
	}
	var dedup spooler.Dedup
	if f := q.Dedup; f != nil {
		if k := f(v); k != "" {
			dedup = spooler.DedupHash(hash(k))
		}
	}
	_, err = q.Client.Send(ctx, spooler.SendRequest{
		Spool: q.Spool,
		Queue: q.Queue,
		Dedup: dedup,
		Data:  data,
	})
	return err
}

// Drain takes messages off the queue until ctx ends, handing each one to fn
// as a T. A message fn returns nil for is acked; an error nacks it,
// [ErrDiscard] discards it and [ErrFail] fails it. The context fn gets ends
// with ctx, and when the message's lease could not be renewed. Drain returns
// the cause of ctx, or the error that stopped it sooner.
func (q *Queue[T]) Drain(ctx context.Context, fn func(context.Context, T) error) error {
	r := receiver[T]{
		client: q.Client,
		spool:  q.Spool,
		queue:  q.Queue,
		decode: q.Decode,

		shutdownTimeout: q.ShutdownTimeout,
	}
	return r.loop(ctx, func(ctx context.Context, v T) (settleFunc, error) {
		err := fn(ctx, v)
		if err != nil {
			return nil, err
		}
		return r.ack, nil
	})
}

func hash(s string) string {
	bts := sha256.Sum256([]byte(s))
	return hex.EncodeToString(bts[:16])
}
