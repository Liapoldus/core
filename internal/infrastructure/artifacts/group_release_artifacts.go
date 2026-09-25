package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type groupReleaseArtifactContract struct {
	ReleaseDirectory string `yaml:"releaseDirectory"`
	StagingDirectory string `yaml:"stagingDirectory"`
	CaddyfileSuffix  string `yaml:"caddyfileSuffix"`
	TemporarySuffix  string `yaml:"temporarySuffix"`
	FileMode         uint32 `yaml:"fileMode"`
	DirectoryMode    uint32 `yaml:"directoryMode"`
	InvalidContract  string `yaml:"invalidContract"`
}

type GroupReleaseArtifacts struct {
	Root     string
	contract groupReleaseArtifactContract
}

var _ interfaces.GroupReleaseArtifactStore = GroupReleaseArtifacts{}

func NewGroupReleaseArtifacts(root string) (GroupReleaseArtifacts, error) {
	contents, err := assets.Contract(assets.GroupReleaseArtifacts)
	if err != nil {
		return GroupReleaseArtifacts{}, err
	}
	var contract groupReleaseArtifactContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return GroupReleaseArtifacts{}, err
	}
	if root == "" || contract.ReleaseDirectory == "" || contract.StagingDirectory == "" || contract.CaddyfileSuffix == "" || contract.TemporarySuffix == "" || contract.FileMode == 0 || contract.DirectoryMode == 0 || contract.InvalidContract == "" {
		return GroupReleaseArtifacts{}, errors.New(contract.InvalidContract)
	}
	return GroupReleaseArtifacts{Root: root, contract: contract}, nil
}

func (store GroupReleaseArtifacts) StageCaddyfile(_ context.Context, revisionID string, contents []byte) (models.GroupRevision, error) {
	if !validRevisionID(revisionID) {
		return models.GroupRevision{}, errors.New(store.contract.InvalidContract)
	}
	root, err := filepath.Abs(store.Root)
	if err != nil {
		return models.GroupRevision{}, err
	}
	if err := os.MkdirAll(root, os.FileMode(store.contract.DirectoryMode)); err != nil {
		return models.GroupRevision{}, err
	}
	releaseDirectory := filepath.Join(root, store.contract.ReleaseDirectory)
	stagingDirectory := filepath.Join(releaseDirectory, store.contract.StagingDirectory)
	if err := os.MkdirAll(stagingDirectory, os.FileMode(store.contract.DirectoryMode)); err != nil {
		return models.GroupRevision{}, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return models.GroupRevision{}, err
	}
	resolvedStaging, err := filepath.EvalSymlinks(stagingDirectory)
	if err != nil || !withinDirectory(resolvedRoot, resolvedStaging) {
		return models.GroupRevision{}, fs.ErrInvalid
	}
	resolvedReleaseDirectory, err := filepath.EvalSymlinks(releaseDirectory)
	if err != nil || !withinDirectory(resolvedRoot, resolvedReleaseDirectory) {
		return models.GroupRevision{}, fs.ErrInvalid
	}
	name := revisionID + store.contract.CaddyfileSuffix
	temporaryPath := filepath.Join(resolvedStaging, revisionID+store.contract.TemporarySuffix)
	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(store.contract.FileMode))
	if err != nil {
		return models.GroupRevision{}, err
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		return models.GroupRevision{}, err
	}
	if err := file.Sync(); err != nil {
		return models.GroupRevision{}, err
	}
	if err := file.Close(); err != nil {
		return models.GroupRevision{}, err
	}
	finalPath := filepath.Join(resolvedReleaseDirectory, name)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return models.GroupRevision{}, err
	}
	removeTemporary = false
	directory, err := os.Open(resolvedReleaseDirectory)
	if err != nil {
		return models.GroupRevision{}, err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return models.GroupRevision{}, syncErr
	}
	if closeErr != nil {
		return models.GroupRevision{}, closeErr
	}
	digest := sha256.Sum256(contents)
	relativePath, err := filepath.Rel(resolvedRoot, finalPath)
	if err != nil || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || relativePath == ".." {
		return models.GroupRevision{}, fs.ErrInvalid
	}
	return models.GroupRevision{ID: revisionID, CaddyfileDigest: hex.EncodeToString(digest[:]), CaddyfilePath: relativePath}, nil
}

func (store GroupReleaseArtifacts) DiscardCaddyfile(_ context.Context, revision models.GroupRevision) error {
	if !validRevisionID(revision.ID) || revision.CaddyfilePath == "" {
		return errors.New(store.contract.InvalidContract)
	}
	root, err := filepath.EvalSymlinks(store.Root)
	if err != nil {
		return err
	}
	path := revision.CaddyfilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
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

func validRevisionID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func withinDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
