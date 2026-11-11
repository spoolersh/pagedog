package spooler

import (
	"context"
	"time"

	"github.com/spoolersh/spooler-go"
)

type lease struct {
	token     spooler.Lease
	cancel    context.CancelFunc
	expiresAt time.Time
	done      <-chan struct{}
}

type leaseKeeper struct {
	client *spooler.Client
	spool  string
	ch     chan lease
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

func startLeaseKeeper(ctx context.Context, c *spooler.Client, s string) *leaseKeeper {
	k := &leaseKeeper{
		client: c,
		spool:  s,
		ch:     make(chan lease, 1),
		done:   make(chan struct{}),
	}
	k.ctx, k.cancel = context.WithCancel(ctx)
	go k.loop()
	return k
}

func (k *leaseKeeper) stop() {
	close(k.ch)
	k.cancel()
	<-k.done
}

func (k *leaseKeeper) loop() {
	defer close(k.done)
leases:
	for lease := range k.ch {
		expiresAt := lease.expiresAt
		for {
			select {
			case <-k.ctx.Done():
				lease.cancel()
				return

			case <-lease.done:
				lease.cancel()
				continue leases

			case <-time.After(time.Until(expiresAt) / 3 * 2):
				// Renew now!
			}
			res, err := k.client.Renew(k.ctx, spooler.RenewRequest{
				Spool: k.spool,
				Lease: lease.token,
			})
			if err != nil {
				lease.cancel()
				break
			}
			expiresAt = res.LeaseExpiresAt
		}
	}
}

// KeepAlive renews the lease of res until the returned stop func is called.
// The context it returns is ctx, ended when a renewal fails. ok is false when
// ctx ended before the keeper took the lease on.
func (k *leaseKeeper) KeepAlive(ctx context.Context, res spooler.RecvResult) (context.Context, func(), bool) {
	sub, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	select {
	case k.ch <- lease{
		cancel:    cancel,
		done:      done,
		token:     res.Lease,
		expiresAt: res.LeaseExpiresAt,
	}:
	case <-ctx.Done():
		return nil, nil, false
	}
	return sub, func() { close(done) }, true
}
