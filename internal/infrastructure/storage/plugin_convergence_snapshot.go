package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

// PluginConvergenceView is a copy of the published desired generations and
// replica observations. Readers never consult the durable source.
type PluginConvergenceView struct {
	Desired map[string]int64
	Records []PluginReplicaObservation
}

// PluginConvergenceSnapshot publishes a complete, immutable status generation.
// Invalidating it makes status fail closed while a durable mutation is being
// reflected into memory or when an observation cannot be persisted.
type PluginConvergenceSnapshot struct {
	configurations interfaces.PluginConfigurationReader
	replicas       *PluginReplicaStore
	instanceIDs    []string
	mu             sync.Mutex
	state          atomic.Pointer[PluginConvergenceView]
}

func NewPluginConvergenceSnapshot(ctx context.Context, configurations interfaces.PluginConfigurationReader, replicas *PluginReplicaStore, instanceIDs []string) (*PluginConvergenceSnapshot, error) {
	if configurations == nil || replicas == nil {
		return nil, models.PluginConfigurationUnavailable{}
	}
	snapshot := &PluginConvergenceSnapshot{
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
func (snapshot *PluginConvergenceSnapshot) Refresh(ctx context.Context) error {
	if snapshot == nil || snapshot.configurations == nil || snapshot.replicas == nil {
		return models.PluginConfigurationUnavailable{}
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	desired := make(map[string]int64, len(snapshot.instanceIDs))
	for _, instanceID := range snapshot.instanceIDs {
		if instanceID == "" {
			snapshot.state.Store(nil)
			return models.PluginConfigurationUnavailable{}
		}
		active, _, err := snapshot.configurations.Current(ctx, instanceID)
		if errors.Is(err, models.PluginConfigurationNotFound{}) {
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
	records, err := snapshot.replicas.List(ctx)
	if err != nil {
		snapshot.state.Store(nil)
		return err
	}
	snapshot.state.Store(&PluginConvergenceView{Desired: desired, Records: records})
	return nil
}

func (snapshot *PluginConvergenceSnapshot) Invalidate() {
	if snapshot != nil {
		snapshot.state.Store(nil)
	}
}

func (snapshot *PluginConvergenceSnapshot) Current() (PluginConvergenceView, error) {
	if snapshot == nil {
		return PluginConvergenceView{}, models.PluginConfigurationUnavailable{}
	}
	view := snapshot.state.Load()
	if view == nil {
		return PluginConvergenceView{}, models.PluginConfigurationUnavailable{}
	}
	desired := make(map[string]int64, len(view.Desired))
	for instanceID, generation := range view.Desired {
		desired[instanceID] = generation
	}
	return PluginConvergenceView{Desired: desired, Records: append([]PluginReplicaObservation(nil), view.Records...)}, nil
}
