package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

// PluginConfigurationSnapshot publishes retained generations in memory. Every
// refresh builds a new map and swaps one pointer, so a pull sees either the old
// complete view or the new one and never consults SQLite.
type PluginConfigurationSnapshot struct {
	source      interfaces.PluginConfigurationReader
	instanceIDs []string
	mu          sync.Mutex
	state       atomic.Pointer[configurationSnapshotState]
}

type configurationSnapshotState struct {
	entries map[string]configurationSnapshotEntry
}

type configurationSnapshotEntry struct {
	active   models.PluginConfigurationRevision
	previous models.PluginConfigurationRevision
	pointers models.PluginConfigurationPointers
}

var _ interfaces.PluginConfigurationReader = (*PluginConfigurationSnapshot)(nil)
var _ interfaces.PluginRetainedConfigurationReader = (*PluginConfigurationSnapshot)(nil)

// NewPluginConfigurationSnapshot loads every declared instance before the
// control listener accepts requests. A corrupt or unavailable store fails the
// whole initialization rather than publishing a partial view.
func NewPluginConfigurationSnapshot(ctx context.Context, source interfaces.PluginConfigurationReader, instanceIDs []string) (*PluginConfigurationSnapshot, error) {
	if source == nil {
		return nil, models.PluginConfigurationUnavailable{}
	}
	entries := make(map[string]configurationSnapshotEntry, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		if instanceID == "" {
			return nil, models.PluginConfigurationUnavailable{}
		}
		entry, found, err := readConfigurationSnapshotEntry(ctx, source, instanceID)
		if err != nil {
			return nil, err
		}
		if found {
			entries[instanceID] = entry
		}
	}
	snapshot := &PluginConfigurationSnapshot{source: source, instanceIDs: append([]string(nil), instanceIDs...)}
	snapshot.state.Store(&configurationSnapshotState{entries: entries})
	return snapshot, nil
}

// RefreshAll reconstructs the complete retained view after startup recovery,
// before either control or Management API begins serving requests.
func (snapshot *PluginConfigurationSnapshot) RefreshAll(ctx context.Context) error {
	if snapshot == nil || snapshot.source == nil {
		return models.PluginConfigurationUnavailable{}
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	entries := make(map[string]configurationSnapshotEntry, len(snapshot.instanceIDs))
	for _, instanceID := range snapshot.instanceIDs {
		entry, found, err := readConfigurationSnapshotEntry(ctx, snapshot.source, instanceID)
		if err != nil {
			snapshot.state.Store(nil)
			return err
		}
		if found {
			entries[instanceID] = entry
		}
	}
	snapshot.state.Store(&configurationSnapshotState{entries: entries})
	return nil
}

// Refresh publishes the durable active/previous pair after a committed
// promotion or rollback and before Reload is sent to any replica.
func (snapshot *PluginConfigurationSnapshot) Refresh(ctx context.Context, instanceID string) error {
	if snapshot == nil || snapshot.source == nil || instanceID == "" {
		return models.PluginConfigurationUnavailable{}
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
	} else {
		delete(entries, instanceID)
	}
	snapshot.state.Store(&configurationSnapshotState{entries: entries})
	return nil
}

func readConfigurationSnapshotEntry(ctx context.Context, source interfaces.PluginConfigurationReader, instanceID string) (configurationSnapshotEntry, bool, error) {
	active, pointers, err := source.Current(ctx, instanceID)
	if errors.Is(err, models.PluginConfigurationNotFound{}) {
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

func (snapshot *PluginConfigurationSnapshot) Current(_ context.Context, instanceID string) (models.PluginConfigurationRevision, models.PluginConfigurationPointers, error) {
	if snapshot == nil || snapshot.state.Load() == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, models.PluginConfigurationUnavailable{}
	}
	entry, found := snapshot.state.Load().entries[instanceID]
	if !found {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
	}
	return copyConfigurationRevision(entry.active), entry.pointers, nil
}

func (snapshot *PluginConfigurationSnapshot) GetRevision(_ context.Context, instanceID string, revision int64) (models.PluginConfigurationRevision, error) {
	if snapshot == nil || snapshot.state.Load() == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{}
	}
	entry, found := snapshot.state.Load().entries[instanceID]
	if !found {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationNotFound{}
	}
	switch revision {
	case entry.active.Revision:
		return copyConfigurationRevision(entry.active), nil
	case entry.previous.Revision:
		if entry.previous.Revision > 0 {
			return copyConfigurationRevision(entry.previous), nil
		}
	}
	return models.PluginConfigurationRevision{}, models.PluginConfigurationNotFound{}
}

// Retained loads the snapshot pointer once, preventing a promotion from
// changing the generation/state pair midway through a config pull.
func (snapshot *PluginConfigurationSnapshot) Retained(_ context.Context, instanceID string, generation int64) (models.PluginConfigurationRevision, bool, error) {
	if snapshot == nil {
		return models.PluginConfigurationRevision{}, false, models.PluginConfigurationUnavailable{}
	}
	state := snapshot.state.Load()
	if state == nil {
		return models.PluginConfigurationRevision{}, false, models.PluginConfigurationUnavailable{}
	}
	entry, found := state.entries[instanceID]
	if !found {
		return models.PluginConfigurationRevision{}, false, models.PluginConfigurationNotFound{}
	}
	switch generation {
	case entry.active.Revision:
		return copyConfigurationRevision(entry.active), true, nil
	case entry.previous.Revision:
		if entry.previous.Revision > 0 {
			return copyConfigurationRevision(entry.previous), false, nil
		}
	}
	return models.PluginConfigurationRevision{}, false, models.PluginConfigurationNotFound{}
}

func copyConfigurationRevision(revision models.PluginConfigurationRevision) models.PluginConfigurationRevision {
	revision.SettingsJSON = append([]byte(nil), revision.SettingsJSON...)
	return revision
}
