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
	"sort"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
)

var registryLocks sync.Map

type FilesystemStore struct {
	root   string
	layout models.RegistryLayout
}

func NewFilesystemStore(root string, layout models.RegistryLayout) FilesystemStore {
	return FilesystemStore{root: root, layout: layout}
}

func (store FilesystemStore) Publish(site, source string) (models.Release, error) {
	unlock := store.lock(site)
	defer unlock()
	manifest, err := os.Lstat(filepath.Join(source, store.layout.Manifest))
	if err != nil || !manifest.Mode().IsRegular() {
		return models.Release{}, errors.New(store.layout.ManifestMissing)
	}
	id, err := sourceDigest(source, store.layout.UnsafeSource)
	if err != nil {
		return models.Release{}, err
	}
	siteRoot := filepath.Join(store.root, store.layout.Sites, site)
	releases := filepath.Join(siteRoot, store.layout.Releases)
	if err = os.MkdirAll(releases, 0750); err != nil {
		return models.Release{}, err
	}
	target := filepath.Join(releases, id)
	if _, statErr := os.Lstat(target); os.IsNotExist(statErr) {
		stage, stageErr := os.MkdirTemp(releases, store.layout.StagePrefix)
		if stageErr != nil {
			return models.Release{}, stageErr
		}
		defer os.RemoveAll(stage)
		if err = copyTree(source, stage, store.layout.UnsafeSource); err != nil {
			return models.Release{}, err
		}
		if err = os.Rename(stage, target); err != nil {
			return models.Release{}, err
		}
	}
	currentPointer := filepath.Join(siteRoot, store.layout.Current)
	previous, previousErr := os.Readlink(currentPointer)
	if previousErr != nil && !errors.Is(previousErr, fs.ErrNotExist) {
		return models.Release{}, previousErr
	}
	if err = store.switchPointers(siteRoot, filepath.Join(store.layout.Releases, id)); err != nil {
		return models.Release{}, err
	}
	release := models.Release{ID: id}
	if previousErr == nil {
		release.PreviousID = filepath.Base(previous)
	}
	return release, nil
}

func (store FilesystemStore) Rollback(site string) (models.Release, error) {
	unlock := store.lock(site)
	defer unlock()
	siteRoot := filepath.Join(store.root, store.layout.Sites, site)
	current, err := os.Readlink(filepath.Join(siteRoot, store.layout.Current))
	if err != nil {
		return models.Release{}, err
	}
	previous, err := os.Readlink(filepath.Join(siteRoot, store.layout.Previous))
	if err != nil {
		return models.Release{}, err
	}
	if _, err = os.Stat(filepath.Join(siteRoot, previous)); err != nil {
		return models.Release{}, err
	}
	if _, err = os.Stat(filepath.Join(siteRoot, current)); err != nil {
		return models.Release{}, err
	}
	if err = store.replacePointer(siteRoot, store.layout.Previous, current); err != nil {
		return models.Release{}, err
	}
	if err = store.replacePointer(siteRoot, store.layout.Current, previous); err != nil {
		return models.Release{}, err
	}
	return models.Release{ID: filepath.Base(previous), PreviousID: filepath.Base(current)}, nil
}

func (store FilesystemStore) Versions(site string) ([]models.Release, error) {
	root := filepath.Join(store.root, store.layout.Sites, site, store.layout.Releases)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return []models.Release{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "" {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	versions := make([]models.Release, 0, len(ids))
	for _, id := range ids {
		versions = append(versions, models.Release{ID: id})
	}
	return versions, nil
}

func (store FilesystemStore) Current(site string) (models.Release, error) {
	return store.pointer(site, store.layout.Current)
}

func (store FilesystemStore) Previous(site string) (models.Release, error) {
	return store.pointer(site, store.layout.Previous)
}

func (store FilesystemStore) pointer(site, pointerName string) (models.Release, error) {
	siteRoot := filepath.Join(store.root, store.layout.Sites, site)
	target, err := os.Readlink(filepath.Join(siteRoot, pointerName))
	if errors.Is(err, fs.ErrNotExist) {
		return models.Release{}, nil
	}
	if err != nil {
		return models.Release{}, err
	}
	cleanTarget := filepath.Clean(target)
	revision := filepath.Base(cleanTarget)
	if cleanTarget != filepath.Join(store.layout.Releases, revision) {
		return models.Release{}, fs.ErrInvalid
	}
	if _, err = os.Stat(filepath.Join(siteRoot, cleanTarget)); err != nil {
		return models.Release{}, err
	}
	return models.Release{ID: revision}, nil
}

func (store FilesystemStore) lock(site string) func() {
	value, _ := registryLocks.LoadOrStore(filepath.Join(store.root, store.layout.Sites, site), &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (store FilesystemStore) switchPointers(siteRoot, current string) error {
	old, err := os.Readlink(filepath.Join(siteRoot, store.layout.Current))
	oldPrevious, previousErr := os.Readlink(filepath.Join(siteRoot, store.layout.Previous))
	if err == nil {
		if err = store.replacePointer(siteRoot, store.layout.Previous, old); err != nil {
			return err
		}
	}
	if err = store.replacePointer(siteRoot, store.layout.Current, current); err != nil {
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

func sourceDigest(root string, unsafeSource string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() && !entry.IsDir() {
			return errors.New(unsafeSource)
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

func copyTree(source, destination, unsafeSource string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() && !entry.IsDir() {
			return errors.New(unsafeSource)
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
