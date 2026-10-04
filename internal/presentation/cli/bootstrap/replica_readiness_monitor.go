package bootstrap

import (
	"context"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

// startReplicaReadinessMonitor samples plugin readiness without retrying Reload.
func startReplicaReadinessMonitor(ctx context.Context, interval time.Duration, applier *plugins.SDKConfigurationApplier) func() {
	workerContext, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workerContext.Done():
				return
			case <-ticker.C:
				_ = applier.ObserveDeclaredReplicaReadiness(workerContext)
			}
		}
	}()
	return func() {
		stop()
		<-done
	}
}
