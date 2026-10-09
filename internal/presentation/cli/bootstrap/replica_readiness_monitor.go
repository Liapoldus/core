package bootstrap

import (
	"context"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

// startRegisteredReplicaReconciler retries only idempotent Reload notifications
// for live registered replicas that have not acknowledged the exact active
// generation.
func startRegisteredReplicaReconciler(ctx context.Context, interval time.Duration, applier *plugins.SDKConfigurationApplier, recover func(context.Context) error) func() {
	workerContext, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = applier.RunRegisteredReconciliation(workerContext, interval, recover)
	}()
	return func() {
		stop()
		<-done
	}
}
