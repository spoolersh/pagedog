package spooler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spoolersh/spooler-go"
)

var (
	// ErrDiscard is the error a [Queue.Drain] or [Pipe.Pump] function
	// returns to have its message discarded instead of nacked.
	ErrDiscard = errors.New("discard")

	// ErrFail is the error a [Queue.Drain] or [Pipe.Pump] function returns
	// to have its message failed instead of nacked.
	ErrFail = errors.New("fail")
)

// receiver is the loop behind [Queue.Drain] and [Pipe.Pump]: it takes each
// message off a queue as a T, hands it to a function that says how to settle
// it, and settles it so.
type receiver[T any] struct {
	client *spooler.Client
	spool  string
	queue  string
	decode func([]byte) (T, error)

	shutdownTimeout time.Duration
}

// settleFunc settles a leased message one way: ack, nack, fail, discard, or
// ack and send.
type settleFunc func(context.Context, spooler.Lease) error

func (r *receiver[T]) ack(ctx context.Context, lease spooler.Lease) error {
	return r.client.Ack(ctx, spooler.AckRequest{
		Spool: r.spool,
		Lease: lease,
	})
}
func (r *receiver[T]) nack(ctx context.Context, lease spooler.Lease) error {
	return r.client.Nack(ctx, spooler.NackRequest{
		Spool: r.spool,
		Lease: lease,
	})
}
func (r *receiver[T]) fail(ctx context.Context, lease spooler.Lease) error {
	return r.client.Fail(ctx, spooler.FailRequest{
		Spool: r.spool,
		Lease: lease,
	})
}
func (r *receiver[T]) discard(ctx context.Context, lease spooler.Lease) error {
	return r.client.Discard(ctx, spooler.DiscardRequest{
		Spool: r.spool,
		Lease: lease,
	})
}
func (r *receiver[T]) release(ctx context.Context, lease spooler.Lease) error {
	return r.client.Release(ctx, spooler.ReleaseRequest{
		Spool: r.spool,
		Lease: lease,
	})
}
func (r *receiver[T]) ackAndSend(req spooler.AckAndSendRequest) func(context.Context, spooler.Lease) error {
	return func(ctx context.Context, lease spooler.Lease) error {
		req.Spool = r.spool
		req.Lease = lease
		_, err := r.client.AckAndSend(ctx, req)
		return err
	}
}

func (r *receiver[T]) loop(ctx context.Context, fn func(context.Context, T) (settleFunc, error)) error {
	k := startLeaseKeeper(ctx, r.client, r.spool)
	defer k.stop()

	sub, cancel := r.context(ctx)
	defer cancel(nil)

	for ctx.Err() == nil {
		m, ok, err := r.client.Recv(sub, spooler.RecvRequest{
			Spool:       r.spool,
			Queue:       r.queue,
			WaitSeconds: new(1),
		})
		if err != nil {
			if cause := context.Cause(sub); cause != nil {
				return fmt.Errorf(
					"%w: %w",
					cause, err,
				)
			}
			return err
		}
		if !ok {
			continue
		}
		if ctx.Err() != nil {
			_ = r.release(sub, m.Lease)
			break
		}

		r.process(ctx, sub, k, m, fn)
	}

	return context.Cause(ctx)
}

func (r *receiver[T]) process(
	ctx context.Context,
	sub context.Context,
	k *leaseKeeper,
	res spooler.RecvResult,
	fn func(context.Context, T) (settleFunc, error),
) {
	item, err := decode(r.decode, res.Data)
	if err != nil {
		// The message encoding is broken: fail it and move on to the next
		// one. We ignore the Fail() call error here to let pump
		// progressing: a lost Fail() leaves the lease to expire, and the
		// message is redelivered until its retries run out.
		_ = r.fail(sub, res.Lease)
		return
	}
	if d := res.LeaseExpiresAt; !d.IsZero() {
		var (
			stop func()
			ok   bool
		)
		ctx, stop, ok = k.KeepAlive(ctx, res)
		if !ok {
			return
		}
		defer stop()
	}
	settle, err := fn(ctx, item)
	switch {
	case errors.Is(err, ErrDiscard):
		settle = r.discard

	case errors.Is(err, ErrFail):
		settle = r.fail

	case err != nil && ctx.Err() != nil:
		// A shutdown or a lost lease is not the message's fault: a release
		// spends no retry.
		settle = r.release

	case err != nil:
		settle = r.nack
	}
	_ = settle(sub, res.Lease)
}

func (r *receiver[T]) context(ctx context.Context) (context.Context, context.CancelCauseFunc) {
	sub, cancel := context.WithCancelCause(context.Background())

	go func() {
		select {
		case <-ctx.Done():
			t := cmp.Or(r.shutdownTimeout, DefaultShutdownTimeout)
			time.AfterFunc(t, func() {
				cancel(ErrShutdownTimeout)
			})

		case <-sub.Done():
			return
		}
	}()

	return sub, cancel
}
