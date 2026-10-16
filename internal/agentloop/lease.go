package agentloop

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrLeaseNil     = errors.New("turn lease is nil")
	ErrLeaseReentry = errors.New("recursive turn acquisition on the same lease is not allowed")
)

type leaseOwnerContextKey struct{}

// Lease serializes complete agent turns while allowing queued callers to
// abandon their wait through context cancellation. A lease marker in the
// returned context prevents a recursive same-owner call from deadlocking.
type Lease struct {
	once  sync.Once
	token chan struct{}
}

func (lease *Lease) Acquire(ctx context.Context) (context.Context, func(), error) {
	if lease == nil {
		return ctx, nil, ErrLeaseNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if owner, _ := ctx.Value(leaseOwnerContextKey{}).(*Lease); owner == lease {
		return ctx, nil, ErrLeaseReentry
	}

	lease.once.Do(func() {
		lease.token = make(chan struct{}, 1)
	})
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	select {
	case lease.token <- struct{}{}:
	case <-ctx.Done():
		return ctx, nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-lease.token
		return ctx, nil, err
	}

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { <-lease.token })
	}
	return context.WithValue(ctx, leaseOwnerContextKey{}, lease), release, nil
}
