package security

import (
	"errors"
	"io"
	"os"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"golang.org/x/sys/unix"
)

var ErrServiceSecretUnavailable = errors.New("service secret unavailable")

// ReadStagedServiceSecret reads one opaque reference from the private staging
// mount. Root and leaf symlinks, paths and non-regular files are rejected.
func ReadStagedServiceSecret(secretRoot, reference string, maximumBytes int64) ([]byte, error) {
	if secretRoot == "" || maximumBytes <= 0 || !models.ValidServiceSecretReferenceID(reference) {
		return nil, ErrServiceSecretUnavailable
	}
	rootFD, err := unix.Open(secretRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrServiceSecretUnavailable
	}
	defer unix.Close(rootFD)
	secretFD, err := unix.Openat(rootFD, reference, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrServiceSecretUnavailable
	}
	file := os.NewFile(uintptr(secretFD), "service-secret")
	if file == nil {
		_ = unix.Close(secretFD)
		return nil, ErrServiceSecretUnavailable
	}
	defer file.Close()
	var information unix.Stat_t
	if err := unix.Fstat(secretFD, &information); err != nil || information.Mode&unix.S_IFMT != unix.S_IFREG || information.Size > maximumBytes {
		return nil, ErrServiceSecretUnavailable
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || int64(len(contents)) > maximumBytes {
		clear(contents)
		return nil, ErrServiceSecretUnavailable
	}
	return contents, nil
}
