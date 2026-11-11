package spooler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spoolersh/spooler-go"
)

// DefaultShutdownTimeout is how long a [Pipe] or a [Queue] keeps talking to
// the server after its context ends.
const DefaultShutdownTimeout = 5 * time.Second

// ErrShutdownTimeout is the error [Pipe.Pump] and [Queue.Drain] return when
// the shutdown timeout cut a request short: the message it was receiving or
// settling may stay leased until the lease expires.
var ErrShutdownTimeout = errors.New("shutdown timeout exceeded")

// Pipe takes messages off an input queue as A, turns each into a B with a
// function of the caller's, and puts it on an output queue.
type Pipe[A, B any] struct {
	Client *spooler.Client
	Spool  string
	Input  string
	Output string

	// Decode turns an input message into an A.
	//
	// Zero means JSON.
	Decode func([]byte) (A, error)

	// Encode turns a B into an output message.
	//
	// Zero means JSON.
	Encode func(B) ([]byte, error)

	// Dedup names an output message for deduplication: the name is hashed
	// into the dedup key the message is sent with. An empty name means no
	// dedup for that message.
	//
	// Zero means no dedup.
	Dedup func(B) string

	// ShutdownTimeout is how long [Pipe.Pump] keeps talking to the server after
	// its context ends, so that a receive in flight completes and its message
	// is released, and a settlement in progress completes.
	//
	// Zero means [DefaultShutdownTimeout].
	ShutdownTimeout time.Duration
}

// Pump receives messages until ctx ends, passing each through fn and on to
// the output queue. A message fn returns a result for is acked as the result
// is sent; an error nacks it, [ErrDiscard] discards it and [ErrFail] fails
// it, as does a result that will not encode. The context fn gets ends with
// ctx, and when the message's lease could not be renewed. Pump returns the
// cause of ctx, or the error that stopped it sooner.
func (p *Pipe[A, B]) Pump(ctx context.Context, fn func(context.Context, A) (B, error)) error {
	r := receiver[A]{
		client: p.Client,
		spool:  p.Spool,
		queue:  p.Input,
		decode: p.Decode,

		shutdownTimeout: p.ShutdownTimeout,
	}
	return r.loop(ctx, func(sub context.Context, in A) (settleFunc, error) {
		out, err := fn(sub, in)
		if err != nil {
			return nil, err
		}
		data, err := encode(p.Encode, out)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrFail, err)
		}
		var dedup spooler.Dedup
		if f := p.Dedup; f != nil {
			if k := f(out); k != "" {
				dedup = spooler.DedupHash(hash(k))
			}
		}
		return r.ackAndSend(spooler.AckAndSendRequest{
			Queue: p.Output,
			Data:  data,
			Dedup: dedup,
		}), nil
	})
}
