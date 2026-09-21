package storage

import (
	"os"
	"reflect"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
)

type MemorySnapshotStore struct {
	mu       sync.RWMutex
	active   models.Snapshot
	prepared []models.PreparedSnapshot
	drained  models.Snapshot
}

func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{}
}

func (store *MemorySnapshotStore) Active() models.Snapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.active
}

func (store *MemorySnapshotStore) Prepare(snapshot models.Snapshot) (models.PreparedSnapshot, error) {
	if reflect.ValueOf(snapshot.Graph.Revision.Value).IsZero() || reflect.ValueOf(snapshot.Graph.Revision.Digest).IsZero() {
		return models.PreparedSnapshot{}, os.ErrInvalid
	}
	prepared := models.NewPreparedSnapshot(snapshot)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.prepared = append(store.prepared, prepared)
	return prepared, nil
}

func (store *MemorySnapshotStore) Activate(prepared models.PreparedSnapshot) (models.Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index, candidate := range store.prepared {
		if !reflect.DeepEqual(candidate.Snapshot(), prepared.Snapshot()) {
			continue
		}
		store.prepared = append(store.prepared[:index], store.prepared[index+1:]...)
		previous := store.active
		store.active = prepared.Snapshot()
		return previous, nil
	}
	return models.Snapshot{}, os.ErrInvalid
}

func (store *MemorySnapshotStore) Drain(snapshot models.Snapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.drained = snapshot
	return nil
}

func (store *MemorySnapshotStore) DrainedRevision() string {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.drained.Graph.Revision.Value
}
