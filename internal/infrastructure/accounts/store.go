package accounts

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var ErrConflict = errors.New("account_conflict")
var ErrNotFound = errors.New("account_not_found")

type Store struct {
	Root      string
	Prefix    string
	Bytes     int
	Cost      int
	Extension string
}

func (s Store) path(id string) string {
	return filepath.Join(s.Root, "secrets", "accounts", id+s.Extension)
}

func (s Store) Create(id string) (string, error) { return s.write(id, false) }
func (s Store) Rotate(id string) (string, error) { return s.write(id, true) }
func (s Store) Revoke(id string) error {
	if !idPattern.MatchString(id) {
		return ErrNotFound
	}
	if err := os.Remove(s.path(id)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
func (s Store) write(id string, replace bool) (string, error) {
	if !idPattern.MatchString(id) {
		return "", ErrNotFound
	}
	p := s.path(id)
	if _, err := os.Stat(p); err == nil && !replace {
		return "", ErrConflict
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	} else if os.IsNotExist(err) && replace {
		return "", ErrNotFound
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", err
	}
	raw := make([]byte, s.Bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := s.Prefix + id + "_" + base64.RawURLEncoding.EncodeToString(raw)
	hash, err := bcrypt.GenerateFromPassword([]byte(key), s.Cost)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".account-")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(hash)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if replace {
		err = os.Rename(tmpName, p)
	} else {
		err = os.Link(tmpName, p)
		if err == nil {
			_ = os.Remove(tmpName)
		}
	}
	if err != nil {
		if os.IsExist(err) {
			return "", ErrConflict
		}
		return "", err
	}
	return key, nil
}

func (s Store) Verify(id, key string) error {
	hash, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(key)); err != nil {
		return fmt.Errorf("invalid bearer: %w", err)
	}
	return nil
}

func (s Store) KeyReference(id string) string {
	return "file:" + strings.TrimPrefix(s.path(id), s.Root+string(os.PathSeparator))
}
