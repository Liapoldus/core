package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
)

const publishLockRecordLimit = 4096

func (store FilesystemStore) acquirePublishLock(site string) (func(), bool, error) {
	fields := store.layout.PublishLockFields
	if store.root == "" || store.layout.Sites == "" || store.layout.PublishLock == "" || fields.PID == "" || fields.StartedAt == "" || fields.Nonce == "" || fields.Lease == "" {
		return nil, false, os.ErrInvalid
	}
	sitesRoot := filepath.Join(store.root, store.layout.Sites)
	if err := os.MkdirAll(sitesRoot, 0750); err != nil {
		return nil, false, err
	}
	if err := regularDirectory(sitesRoot); err != nil {
		return nil, false, err
	}
	siteRelative := filepath.Clean(site)
	if !filepath.IsLocal(site) || siteRelative == filepath.Clean("") || filepath.Base(siteRelative) != siteRelative {
		return nil, false, os.ErrInvalid
	}
	siteRoot := filepath.Join(sitesRoot, siteRelative)
	if err := os.MkdirAll(siteRoot, 0750); err != nil {
		return nil, false, err
	}
	if err := regularDirectory(siteRoot); err != nil {
		return nil, false, err
	}
	path := filepath.Join(siteRoot, store.layout.PublishLock)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, false, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, false, models.NewRegistryLockConflict(store.layout.PublishInProgress)
		}
		return nil, false, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		if err != nil {
			return nil, false, err
		}
		return nil, false, os.ErrInvalid
	}
	if err = file.Chmod(0600); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, false, err
	}
	abort := func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}
	unlock := func() {
		_ = file.Truncate(0)
		_, _ = file.Seek(0, io.SeekStart)
		_ = file.Sync()
		abort()
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		abort()
		return nil, false, err
	}
	contents, err := io.ReadAll(io.LimitReader(file, publishLockRecordLimit+1))
	if err != nil {
		abort()
		return nil, false, err
	}
	if len(contents) > publishLockRecordLimit {
		abort()
		return nil, false, models.NewRegistryLockConflict(store.layout.PublishInProgress)
	}
	recovered := false
	if len(contents) != 0 {
		stale, valid := store.publishLockIsStale(contents)
		if !valid || !stale {
			abort()
			return nil, false, models.NewRegistryLockConflict(store.layout.PublishInProgress)
		}
		recovered = true
	}
	var nonceBytes [16]byte
	if _, err = rand.Read(nonceBytes[:]); err != nil {
		abort()
		return nil, recovered, err
	}
	record := map[string]any{
		fields.PID:       os.Getpid(),
		fields.StartedAt: time.Now().UTC(),
		fields.Nonce:     hex.EncodeToString(nonceBytes[:]),
		fields.Lease:     store.layout.PublishLockLease,
	}
	encoded, err := json.Marshal(record)
	if err == nil {
		if err = file.Truncate(0); err == nil {
			_, err = file.Seek(0, io.SeekStart)
		}
	}
	if err == nil {
		_, err = file.Write(encoded)
	}
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		unlock()
		return nil, recovered, err
	}
	return unlock, recovered, nil
}

func regularDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return os.ErrInvalid
	}
	return nil
}

func (store FilesystemStore) publishLockIsStale(contents []byte) (bool, bool) {
	fields := store.layout.PublishLockFields
	var record map[string]json.RawMessage
	if err := json.Unmarshal(contents, &record); err != nil || record == nil {
		return false, false
	}
	lease := store.layout.PublishLockLease
	if value := record[fields.Lease]; len(value) != 0 {
		if err := json.Unmarshal(value, &lease); err != nil {
			return false, false
		}
	}
	duration, err := time.ParseDuration(lease)
	if err != nil || duration <= 0 {
		return false, false
	}
	var startedAt time.Time
	if value := record[fields.StartedAt]; len(value) != 0 {
		if err := json.Unmarshal(value, &startedAt); err != nil {
			return false, false
		}
	}
	var pid int
	if value := record[fields.PID]; len(value) != 0 {
		if err := json.Unmarshal(value, &pid); err != nil {
			var text string
			if stringErr := json.Unmarshal(value, &text); stringErr != nil {
				return false, false
			}
			pid, err = strconv.Atoi(text)
			if err != nil {
				pid = 0
			}
		}
	}
	if pid > 0 && !processExists(pid) {
		return true, true
	}
	if !startedAt.IsZero() && !time.Now().Before(startedAt.Add(duration)) {
		return true, true
	}
	return false, true
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
