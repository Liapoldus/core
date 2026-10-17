package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceConfigurationSnapshot publishes retained generations in memory. Every
// refresh builds a new map and swaps one pointer, so a pull sees either the old
// complete view or the new one and never consults SQLite.
type ServiceConfigurationSnapshot struct {
	source      interfaces.ServiceConfigurationReader
	instanceIDs []string
	mu          sync.Mutex
	state       atomic.Pointer[configurationSnapshotState]
}

type configurationSnapshotState struct {
	entries map[string]configurationSnapshotEntry
}

type configurationSnapshotEntry struct {
	active   models.ServiceConfigurationRevision
	previous models.ServiceConfigurationRevision
	pointers models.ServiceConfigurationPointers
}

var _ interfaces.ServiceConfigurationReader = (*ServiceConfigurationSnapshot)(nil)
var _ interfaces.ServiceRetainedConfigurationReader = (*ServiceConfigurationSnapshot)(nil)

// NewServiceConfigurationSnapshot loads every declared instance before the
// control listener accepts requests. A corrupt or unavailable store fails the
// whole initialization rather than publishing a partial view.
func NewServiceConfigurationSnapshot(ctx context.Context, source interfaces.ServiceConfigurationReader, instanceIDs []string) (*ServiceConfigurationSnapshot, error) {
	if source == nil {
		return nil, models.ServiceConfigurationUnavailable{}
	}
	instanceIDs, err := configurationInstanceIDs(ctx, source, instanceIDs)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]configurationSnapshotEntry, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		if instanceID == "" {
			return nil, models.ServiceConfigurationUnavailable{}
		}
		entry, found, err := readConfigurationSnapshotEntry(ctx, source, instanceID)
		if err != nil {
			return nil, err
		}
		if found {
			entries[instanceID] = entry
		}
	}
	snapshot := &ServiceConfigurationSnapshot{source: source, instanceIDs: append([]string(nil), instanceIDs...)}
	snapshot.state.Store(&configurationSnapshotState{entries: entries})
	return snapshot, nil
}

// RefreshAll reconstructs the complete retained view after startup recovery,
// before either control or Management API begins serving requests.
func (snapshot *ServiceConfigurationSnapshot) RefreshAll(ctx context.Context) error {
	if snapshot == nil || snapshot.source == nil {
		return models.ServiceConfigurationUnavailable{}
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	instanceIDs, err := configurationInstanceIDs(ctx, snapshot.source, snapshot.instanceIDs)
	if err != nil {
		snapshot.state.Store(nil)
		return err
	}
	entries := make(map[string]configurationSnapshotEntry, len(snapshot.instanceIDs))
	for _, instanceID := range instanceIDs {
		entry, found, err := readConfigurationSnapshotEntry(ctx, snapshot.source, instanceID)
		if err != nil {
			snapshot.state.Store(nil)
			return err
		}
		if found {
			entries[instanceID] = entry
		}
	}
	snapshot.instanceIDs = append([]string(nil), instanceIDs...)
	snapshot.state.Store(&configurationSnapshotState{entries: entries})
	return nil
}

// Refresh publishes the durable active/previous pair after a committed
// promotion or rollback and before Reload is sent to any replica.
func (snapshot *ServiceConfigurationSnapshot) Refresh(ctx context.Context, instanceID string) error {
	if snapshot == nil || snapshot.source == nil || instanceID == "" {
		return models.ServiceConfigurationUnavailable{}
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	entry, found, err := readConfigurationSnapshotEntry(ctx, snapshot.source, instanceID)
	if err != nil {
		return err
	}
	previous := snapshot.state.Load()
	entries := make(map[string]configurationSnapshotEntry, len(previous.entries)+1)
	for id, retained := range previous.entries {
		entries[id] = retained
	}
	if found {
		entries[instanceID] = entry
		if !containsInstanceID(snapshot.instanceIDs, instanceID) {
			snapshot.instanceIDs = append(snapshot.instanceIDs, instanceID)
		}
	} else {
		delete(entries, instanceID)
	}
	snapshot.state.Store(&configurationSnapshotState{entries: entries})
	return nil
}

func configurationInstanceIDs(ctx context.Context, source interfaces.ServiceConfigurationReader, fallback []string) ([]string, error) {
	if lister, ok := source.(interfaces.ServiceConfigurationInstanceLister); ok {
		return lister.ListActiveInstances(ctx)
	}
	return append([]string(nil), fallback...), nil
}

func containsInstanceID(instanceIDs []string, target string) bool {
	for _, instanceID := range instanceIDs {
		if instanceID == target {
			return true
		}
	}
	return false
}

func readConfigurationSnapshotEntry(ctx context.Context, source interfaces.ServiceConfigurationReader, instanceID string) (configurationSnapshotEntry, bool, error) {
	active, pointers, err := source.Current(ctx, instanceID)
	if errors.Is(err, models.ServiceConfigurationNotFound{}) {
		return configurationSnapshotEntry{}, false, nil
	}
	if err != nil {
		return configurationSnapshotEntry{}, false, err
	}
	if active.Revision == 0 {
		return configurationSnapshotEntry{}, false, nil
	}
	entry := configurationSnapshotEntry{active: copyConfigurationRevision(active), pointers: pointers}
	entry.pointers.PendingRevision = 0
	if pointers.PreviousRevision > 0 {
		previous, err := source.GetRevision(ctx, instanceID, pointers.PreviousRevision)
		if err != nil {
			return configurationSnapshotEntry{}, false, err
		}
		entry.previous = copyConfigurationRevision(previous)
	}
	return entry, true, nil
}

func (snapshot *ServiceConfigurationSnapshot) Current(_ context.Context, instanceID string) (models.ServiceConfigurationRevision, models.ServiceConfigurationPointers, error) {
	if snapshot == nil || snapshot.state.Load() == nil {
		return models.ServiceConfigurationRevision{}, models.ServiceConfigurationPointers{}, models.ServiceConfigurationUnavailable{}
	}
	entry, found := snapshot.state.Load().entries[instanceID]
	if !found {
		return models.ServiceConfigurationRevision{}, models.ServiceConfigurationPointers{}, models.ServiceConfigurationNotFound{}
	}
	return copyConfigurationRevision(entry.active), entry.pointers, nil
}

func (snapshot *ServiceConfigurationSnapshot) GetRevision(_ context.Context, instanceID string, revision int64) (models.ServiceConfigurationRevision, error) {
	if snapshot == nil || snapshot.state.Load() == nil {
		return models.ServiceConfigurationRevision{}, models.ServiceConfigurationUnavailable{}
	}
	entry, found := snapshot.state.Load().entries[instanceID]
	if !found {
		return models.ServiceConfigurationRevision{}, models.ServiceConfigurationNotFound{}
	}
	switch revision {
	case entry.active.Revision:
		return copyConfigurationRevision(entry.active), nil
	case entry.previous.Revision:
		if entry.previous.Revision > 0 {
			return copyConfigurationRevision(entry.previous), nil
		}
	}
	return models.ServiceConfigurationRevision{}, models.ServiceConfigurationNotFound{}
}

// Retained loads the snapshot pointer once, preventing a promotion from
// changing the generation/state pair midway through a config pull.
func (snapshot *ServiceConfigurationSnapshot) Retained(_ context.Context, instanceID string, generation int64) (models.ServiceConfigurationRevision, bool, error) {
	if snapshot == nil {
		return models.ServiceConfigurationRevision{}, false, models.ServiceConfigurationUnavailable{}
	}
	state := snapshot.state.Load()
	if state == nil {
		return models.ServiceConfigurationRevision{}, false, models.ServiceConfigurationUnavailable{}
	}
	entry, found := state.entries[instanceID]
	if !found {
		return models.ServiceConfigurationRevision{}, false, models.ServiceConfigurationNotFound{}
	}
	switch generation {
	case entry.active.Revision:
		return copyConfigurationRevision(entry.active), true, nil
	case entry.previous.Revision:
		if entry.previous.Revision > 0 {
			return copyConfigurationRevision(entry.previous), false, nil
		}
	}
	return models.ServiceConfigurationRevision{}, false, models.ServiceConfigurationNotFound{}
}

func copyConfigurationRevision(revision models.ServiceConfigurationRevision) models.ServiceConfigurationRevision {
	revision.SettingsJSON = append([]byte(nil), revision.SettingsJSON...)
	return revision
}
