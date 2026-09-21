// Package storage contains filesystem persistence adapters.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
)

var registryLocks sync.Map

type FilesystemStore struct{ root string }

func NewFilesystemStore(root string) FilesystemStore { return FilesystemStore{root: root} }

func (store FilesystemStore) Publish(site, source string) (models.Release, error) {
	unlock := store.lock(site)
	defer unlock()
	manifest, err := os.Lstat(filepath.Join(source, "site.yaml"))
	if err != nil || !manifest.Mode().IsRegular() {
		return models.Release{}, errors.New("site manifest missing")
	}
	id, err := sourceDigest(source)
	if err != nil {
		return models.Release{}, err
	}
	siteRoot := filepath.Join(store.root, "sites", site)
	releases := filepath.Join(siteRoot, "releases")
	if err = os.MkdirAll(releases, 0750); err != nil {
		return models.Release{}, err
	}
	target := filepath.Join(releases, id)
	if _, statErr := os.Lstat(target); os.IsNotExist(statErr) {
		stage, stageErr := os.MkdirTemp(releases, ".stage-")
		if stageErr != nil {
			return models.Release{}, stageErr
		}
		defer os.RemoveAll(stage)
		if err = copyTree(source, stage); err != nil {
			return models.Release{}, err
		}
		if err = os.Rename(stage, target); err != nil {
			return models.Release{}, err
		}
	}
	if err = store.switchPointers(siteRoot, filepath.Join("releases", id)); err != nil {
		return models.Release{}, err
	}
	return models.Release{ID: id}, nil
}

func (store FilesystemStore) Rollback(site string) (models.Release, error) {
	unlock := store.lock(site)
	defer unlock()
	siteRoot := filepath.Join(store.root, "sites", site)
	current, err := os.Readlink(filepath.Join(siteRoot, "current"))
	if err != nil {
		return models.Release{}, err
	}
	previous, err := os.Readlink(filepath.Join(siteRoot, "previous"))
	if err != nil {
		return models.Release{}, err
	}
	if err = store.replacePointer(siteRoot, "previous", current); err != nil {
		return models.Release{}, err
	}
	if err = store.replacePointer(siteRoot, "current", previous); err != nil {
		return models.Release{}, err
	}
	return models.Release{ID: filepath.Base(previous)}, nil
}

func (store FilesystemStore) lock(site string) func() {
	value, _ := registryLocks.LoadOrStore(filepath.Join(store.root, site), &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (store FilesystemStore) switchPointers(siteRoot, current string) error {
	old, err := os.Readlink(filepath.Join(siteRoot, "current"))
	oldPrevious, previousErr := os.Readlink(filepath.Join(siteRoot, "previous"))
	if err == nil {
		if err = store.replacePointer(siteRoot, "previous", old); err != nil {
			return err
		}
	}
	if err = store.replacePointer(siteRoot, "current", current); err != nil {
		return err
	}
	if previousErr == nil && oldPrevious != current && oldPrevious != old {
		return os.RemoveAll(filepath.Join(siteRoot, oldPrevious))
	}
	return nil
}

func (store FilesystemStore) replacePointer(siteRoot, name, target string) error {
	temporary := filepath.Join(siteRoot, "."+name+".next")
	_ = os.Remove(temporary)
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(siteRoot, name))
}

func sourceDigest(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() && !entry.IsDir() {
			return errors.New("unsafe source entry")
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		_, _ = io.WriteString(hash, relative)
		if entry.Type().IsRegular() {
			file, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() && !entry.IsDir() {
			return errors.New("unsafe source entry")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0750)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, input)
		closeErr := output.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
