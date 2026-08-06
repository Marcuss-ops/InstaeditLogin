package worker

import (
	"context"
	"time"
)

func startReconcileLeaseHeartbeat(
	ctx context.Context,
	store ReconcilePostStore,
	targetID int64,
	ownerID string,
	leaseTTL time.Duration,
) (context.Context, func()) {
	leaseCtx, cancel := context.WithCancel(ctx)
	interval := leaseTTL / 3
	if interval <= 0 {
		interval = time.Second
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				if err := store.HeartbeatReconcileTarget(targetID, ownerID, leaseTTL); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return leaseCtx, func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		cancel()
		<-done
	}
}
