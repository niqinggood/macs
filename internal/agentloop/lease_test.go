package agentloop

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLeaseRejectsReentry(t *testing.T) {
	lease := &Lease{}
	ctx, release, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	if _, _, err := lease.Acquire(ctx); !errors.Is(err, ErrLeaseReentry) {
		t.Fatalf("reentry error = %v, want %v", err, ErrLeaseReentry)
	}
}

func TestLeaseWaitHonorsCancellation(t *testing.T) {
	lease := &Lease{}
	_, release, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := lease.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}
}

func TestDistinctLeasesDoNotBlockEachOther(t *testing.T) {
	first := &Lease{}
	second := &Lease{}
	_, releaseFirst, err := first.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	defer releaseFirst()
	_, releaseSecond, err := second.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire second: %v", err)
	}
	releaseSecond()
}
