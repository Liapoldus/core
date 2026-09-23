package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
)

var auditLocks sync.Map

type FilesystemAuditStore struct {
	directory  string
	extension  string
	dateLayout string
}

func NewFilesystemAuditStore(root, directory, extension, dateLayout string) (FilesystemAuditStore, error) {
	if root == "" || directory == "" || extension == "" || dateLayout == "" || filepath.Base(directory) != directory || filepath.Base(extension) != extension {
		return FilesystemAuditStore{}, os.ErrInvalid
	}
	return FilesystemAuditStore{
		directory:  filepath.Join(root, directory),
		extension:  extension,
		dateLayout: dateLayout,
	}, nil
}

func (store FilesystemAuditStore) Append(ctx context.Context, record models.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	unlock := store.lock()
	defer unlock()
	if err := os.MkdirAll(store.directory, 0750); err != nil {
		return err
	}
	path := store.path(record.Timestamp)
	if info, statErr := os.Lstat(path); statErr == nil && !info.Mode().IsRegular() {
		return os.ErrInvalid
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if statErr != nil {
			return statErr
		}
		return os.ErrInvalid
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	encoder := json.NewEncoder(file)
	encodeErr := encoder.Encode(record)
	syncErr := file.Sync()
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (store FilesystemAuditStore) List(ctx context.Context, cutoff time.Time) ([]models.AuditRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unlock := store.lock()
	defer unlock()
	entries, err := os.ReadDir(store.directory)
	if os.IsNotExist(err) {
		return []models.AuditRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	cutoffDate := cutoff.UTC().Format(store.dateLayout)
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), store.extension) {
			continue
		}
		date := strings.TrimSuffix(entry.Name(), store.extension)
		parsed, parseErr := time.Parse(store.dateLayout, date)
		if parseErr != nil {
			continue
		}
		if parsed.UTC().Format(store.dateLayout) < cutoffDate {
			if removeErr := os.Remove(filepath.Join(store.directory, entry.Name())); removeErr != nil {
				return nil, removeErr
			}
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	records := make([]models.AuditRecord, 0)
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, openErr := os.Open(filepath.Join(store.directory, name))
		if openErr != nil {
			return nil, openErr
		}
		reader := bufio.NewReader(file)
		for {
			line, readErr := reader.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) != 0 {
				var record models.AuditRecord
				if decodeErr := json.Unmarshal(line, &record); decodeErr != nil {
					_ = file.Close()
					return nil, decodeErr
				}
				if !record.Timestamp.Before(cutoff) {
					records = append(records, record)
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					_ = file.Close()
					return nil, readErr
				}
				break
			}
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Timestamp.Before(records[j].Timestamp) })
	return records, nil
}

func (store FilesystemAuditStore) path(timestamp time.Time) string {
	return filepath.Join(store.directory, timestamp.UTC().Format(store.dateLayout)+store.extension)
}

func (store FilesystemAuditStore) lock() func() {
	value, _ := auditLocks.LoadOrStore(store.directory, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
