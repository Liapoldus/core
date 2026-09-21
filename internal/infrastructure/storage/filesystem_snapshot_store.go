package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
)

type FilesystemSnapshotStore struct {
	mu     sync.Mutex
	root   string
	layout models.SnapshotLayout
}

func NewFilesystemSnapshotStore(root string, layout models.SnapshotLayout) *FilesystemSnapshotStore {
	return &FilesystemSnapshotStore{root: root, layout: layout}
}

func (store *FilesystemSnapshotStore) Active() models.Snapshot {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.read(store.activePath())
}

func (store *FilesystemSnapshotStore) Prepare(snapshot models.Snapshot) (models.PreparedSnapshot, error) {
	if reflect.ValueOf(snapshot.Graph.Revision.Value).IsZero() || reflect.ValueOf(snapshot.Graph.Revision.Digest).IsZero() {
		return models.PreparedSnapshot{}, os.ErrInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := os.MkdirAll(store.root, 0750); err != nil {
		return models.PreparedSnapshot{}, err
	}
	file, err := os.CreateTemp(store.root, store.layout.PreparedPrefix)
	if err != nil {
		return models.PreparedSnapshot{}, err
	}
	if err := store.write(file, snapshot); err != nil {
		_ = os.Remove(file.Name())
		_ = file.Close()
		return models.PreparedSnapshot{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return models.PreparedSnapshot{}, err
	}
	return models.NewPreparedSnapshot(snapshot), nil
}

func (store *FilesystemSnapshotStore) Activate(prepared models.PreparedSnapshot) (models.Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	target := ""
	for _, candidate := range store.preparedPaths() {
		if reflect.DeepEqual(store.read(candidate), prepared.Snapshot()) {
			target = candidate
			break
		}
	}
	if target == "" {
		return models.Snapshot{}, os.ErrInvalid
	}
	previous := store.read(store.activePath())
	if err := store.atomicWrite(store.activePath(), prepared.Snapshot()); err != nil {
		return models.Snapshot{}, err
	}
	_ = os.Remove(target)
	return previous, nil
}

func (store *FilesystemSnapshotStore) Drain(snapshot models.Snapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.atomicWrite(store.drainedPath(), snapshot)
}

func (store *FilesystemSnapshotStore) DrainedRevision() string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.read(store.drainedPath()).Graph.Revision.Value
}

func (store *FilesystemSnapshotStore) write(file *os.File, snapshot models.Snapshot) error {
	contents, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err := os.Chmod(file.Name(), 0640); err != nil {
		return err
	}
	_, err = file.Write(contents)
	return err
}

func (store *FilesystemSnapshotStore) atomicWrite(target string, snapshot models.Snapshot) error {
	if err := os.MkdirAll(store.root, 0750); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".next")
	_ = os.Remove(temporary)
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	if err := store.write(file, snapshot); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, target)
}

func (store *FilesystemSnapshotStore) preparedPaths() []string {
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), store.layout.PreparedPrefix) {
			paths = append(paths, filepath.Join(store.root, entry.Name()))
		}
	}
	return paths
}

func (store *FilesystemSnapshotStore) read(path string) models.Snapshot {
	contents, err := os.ReadFile(path)
	if err != nil {
		return models.Snapshot{}
	}
	var snapshot models.Snapshot
	if err := json.Unmarshal(contents, &snapshot); err != nil {
		return models.Snapshot{}
	}
	return snapshot
}

func (store *FilesystemSnapshotStore) activePath() string {
	return filepath.Join(store.root, store.layout.Active)
}

func (store *FilesystemSnapshotStore) drainedPath() string {
	return filepath.Join(store.root, store.layout.Drained)
}