package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

type CaddyCheckpointOptions struct {
	Directory            string
	CheckpointSuffix     string
	TemporarySuffix      string
	DirectoryMode        uint32
	FileMode             uint32
	InvalidConfiguration string
}

type CaddyCheckpointArtifacts struct {
	root    string
	options CaddyCheckpointOptions
}

func NewCaddyCheckpointArtifacts(root string, options CaddyCheckpointOptions) (*CaddyCheckpointArtifacts, error) {
	if root == "" || options.Directory == "" || options.CheckpointSuffix == "" || options.TemporarySuffix == "" || options.DirectoryMode == 0 || options.FileMode == 0 || options.InvalidConfiguration == "" {
		return nil, errors.New(options.InvalidConfiguration)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &CaddyCheckpointArtifacts{root: absolute, options: options}, nil
}

func (store *CaddyCheckpointArtifacts) Store(ctx context.Context, id string, contents []byte) (string, string, error) {
	if store == nil {
		return "", "", fs.ErrInvalid
	}
	if id == "" || len(contents) == 0 {
		return "", "", errors.New(store.options.InvalidConfiguration)
	}
	decodedID, err := hex.DecodeString(id)
	if err != nil || len(decodedID) != 16 {
		return "", "", errors.New(store.options.InvalidConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(store.root, os.FileMode(store.options.DirectoryMode)); err != nil {
		return "", "", err
	}
	root, err := filepath.EvalSymlinks(store.root)
	if err != nil {
		return "", "", err
	}
	directory := filepath.Join(root, store.options.Directory)
	if err := os.MkdirAll(directory, os.FileMode(store.options.DirectoryMode)); err != nil {
		return "", "", err
	}
	if err := os.Chmod(directory, os.FileMode(store.options.DirectoryMode)); err != nil {
		return "", "", err
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil || !withinDirectory(root, resolvedDirectory) {
		return "", "", fs.ErrInvalid
	}
	temporaryPath := filepath.Join(resolvedDirectory, id+store.options.TemporarySuffix)
	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(store.options.FileMode))
	if err != nil {
		return "", "", err
	}
	finalize := false
	defer func() {
		_ = file.Close()
		if !finalize {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		return "", "", err
	}
	if err := file.Sync(); err != nil {
		return "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", err
	}
	finalPath := filepath.Join(resolvedDirectory, id+store.options.CheckpointSuffix)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return "", "", err
	}
	finalize = true
	directoryHandle, err := os.Open(resolvedDirectory)
	if err != nil {
		return "", "", err
	}
	syncErr := directoryHandle.Sync()
	closeErr := directoryHandle.Close()
	if (syncErr != nil && !errors.Is(syncErr, syscall.EINVAL)) || closeErr != nil {
		return "", "", errors.Join(syncErr, closeErr)
	}
	relative, err := filepath.Rel(root, finalPath)
	if err != nil || !filepath.IsLocal(relative) {
		return "", "", fs.ErrInvalid
	}
	digest := sha256.Sum256(contents)
	return relative, hex.EncodeToString(digest[:]), nil
}

func (store *CaddyCheckpointArtifacts) Delete(ctx context.Context, relativePath string) error {
	if store == nil {
		return fs.ErrInvalid
	}
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return errors.New(store.options.InvalidConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(store.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	path := filepath.Join(root, filepath.Clean(relativePath))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !withinDirectory(root, resolved) {
		return fs.ErrInvalid
	}
	return os.Remove(resolved)
}
