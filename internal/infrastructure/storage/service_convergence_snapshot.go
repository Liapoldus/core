package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceConvergenceView is a copy of the published desired generations and
// replica observations. Readers never consult the durable source.
type ServiceConvergenceView struct {
	Desired map[string]int64
	Records []ServiceReplicaObservation
}

// ServiceConvergenceSnapshot publishes a complete, immutable status generation.
// Invalidating it makes status fail closed while a durable mutation is being
// reflected into memory or when an observation cannot be persisted.
type ServiceConvergenceSnapshot struct {
	configurations interfaces.ServiceConfigurationReader
	replicas       *ServiceReplicaStore
	instanceIDs    []string
	mu             sync.Mutex
	state          atomic.Pointer[ServiceConvergenceView]
}

func NewServiceConvergenceSnapshot(ctx context.Context, configurations interfaces.ServiceConfigurationReader, replicas *ServiceReplicaStore, instanceIDs []string) (*ServiceConvergenceSnapshot, error) {
	if configurations == nil || replicas == nil {
		return nil, models.ServiceConfigurationUnavailable{}
	}
	snapshot := &ServiceConvergenceSnapshot{
		configurations: configurations,
		replicas:       replicas,
		instanceIDs:    append([]string(nil), instanceIDs...),
	}
	if err := snapshot.Refresh(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Refresh reads durable state only on startup or a mutation path, then swaps
// one pointer. Any read failure invalidates the view rather than serving stale
// readiness as if the desired generation had not changed.
func (snapshot *ServiceConvergenceSnapshot) Refresh(ctx context.Context) error {
	if snapshot == nil || snapshot.configurations == nil || snapshot.replicas == nil {
		return models.ServiceConfigurationUnavailable{}
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	instanceIDs, err := configurationInstanceIDs(ctx, snapshot.configurations, snapshot.instanceIDs)
	if err != nil {
		snapshot.state.Store(nil)
		return err
	}
	desired := make(map[string]int64, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		if instanceID == "" {
			snapshot.state.Store(nil)
			return models.ServiceConfigurationUnavailable{}
		}
		active, _, err := snapshot.configurations.Current(ctx, instanceID)
		if errors.Is(err, models.ServiceConfigurationNotFound{}) {
			continue
		}
		if err != nil {
			snapshot.state.Store(nil)
			return err
		}
		if active.Revision > 0 {
			desired[instanceID] = active.Revision
		}
	}
	snapshot.instanceIDs = append([]string(nil), instanceIDs...)
	records, err := snapshot.replicas.List(ctx)
	if err != nil {
		snapshot.state.Store(nil)
		return err
	}
	snapshot.state.Store(&ServiceConvergenceView{Desired: desired, Records: records})
	return nil
}

func (snapshot *ServiceConvergenceSnapshot) Invalidate() {
	if snapshot != nil {
		snapshot.state.Store(nil)
	}
}

func (snapshot *ServiceConvergenceSnapshot) Current() (ServiceConvergenceView, error) {
	if snapshot == nil {
		return ServiceConvergenceView{}, models.ServiceConfigurationUnavailable{}
	}
	view := snapshot.state.Load()
	if view == nil {
		return ServiceConvergenceView{}, models.ServiceConfigurationUnavailable{}
	}
	desired := make(map[string]int64, len(view.Desired))
	for instanceID, generation := range view.Desired {
		desired[instanceID] = generation
	}
	return ServiceConvergenceView{Desired: desired, Records: append([]ServiceReplicaObservation(nil), view.Records...)}, nil
}
