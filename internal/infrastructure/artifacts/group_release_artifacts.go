package artifacts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

type groupReleaseArtifactContract struct {
	ReleaseDirectory        string `yaml:"releaseDirectory"`
	StagingDirectory        string `yaml:"stagingDirectory"`
	CaddyfileSuffix         string `yaml:"caddyfileSuffix"`
	TemporarySuffix         string `yaml:"temporarySuffix"`
	ArtifactTemporarySuffix string `yaml:"artifactTemporarySuffix"`
	ArtifactDirectorySuffix string `yaml:"artifactDirectorySuffix"`
	SourceArchiveName       string `yaml:"sourceArchiveName"`
	FileMode                uint32 `yaml:"fileMode"`
	DirectoryMode           uint32 `yaml:"directoryMode"`
	InvalidContract         string `yaml:"invalidContract"`
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
	if root == "" || contract.ReleaseDirectory == "" || contract.StagingDirectory == "" || contract.CaddyfileSuffix == "" || contract.TemporarySuffix == "" || contract.ArtifactTemporarySuffix == "" || contract.ArtifactDirectorySuffix == "" || contract.SourceArchiveName == "" || contract.FileMode == 0 || contract.DirectoryMode == 0 || contract.InvalidContract == "" {
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

func (store GroupReleaseArtifacts) StageArtifact(_ context.Context, revisionID string, contents []byte, policy models.GroupReleasePolicy) (models.GroupRevision, error) {
	if !validRevisionID(revisionID) || len(contents) == 0 {
		return models.GroupRevision{}, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
	}
	if int64(len(contents)) > policy.CompressedArtifactLimitBytes {
		return models.GroupRevision{}, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration), TooLarge: true}
	}
	root, err := filepath.Abs(store.Root)
	if err != nil {
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
	resolvedRelease, err := filepath.EvalSymlinks(releaseDirectory)
	if err != nil || !withinDirectory(resolvedRoot, resolvedRelease) {
		return models.GroupRevision{}, fs.ErrInvalid
	}
	temporaryRoot := filepath.Join(resolvedStaging, revisionID+store.contract.ArtifactTemporarySuffix)
	if err := os.Mkdir(temporaryRoot, os.FileMode(store.contract.DirectoryMode)); err != nil {
		return models.GroupRevision{}, err
	}
	keepStaging := false
	stagedRoot := temporaryRoot
	defer func() {
		if !keepStaging {
			_ = os.RemoveAll(stagedRoot)
		}
	}()
	archiveFile, err := os.OpenFile(filepath.Join(temporaryRoot, store.contract.SourceArchiveName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(store.contract.FileMode))
	if err != nil {
		return models.GroupRevision{}, err
	}
	if _, err := archiveFile.Write(contents); err != nil {
		_ = archiveFile.Close()
		return models.GroupRevision{}, err
	}
	if err := archiveFile.Sync(); err != nil {
		_ = archiveFile.Close()
		return models.GroupRevision{}, err
	}
	if err := archiveFile.Close(); err != nil {
		return models.GroupRevision{}, err
	}
	frontends, err := extractFrontendArchive(temporaryRoot, contents, policy)
	if err != nil {
		return models.GroupRevision{}, err
	}
	finalRoot := filepath.Join(resolvedRelease, revisionID+store.contract.ArtifactDirectorySuffix)
	if err := os.Rename(temporaryRoot, finalRoot); err != nil {
		return models.GroupRevision{}, err
	}
	stagedRoot = finalRoot
	if directory, openErr := os.Open(resolvedRelease); openErr == nil {
		syncErr := directory.Sync()
		closeErr := directory.Close()
		if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
			return models.GroupRevision{}, syncErr
		}
		if closeErr != nil {
			return models.GroupRevision{}, closeErr
		}
	} else {
		return models.GroupRevision{}, openErr
	}
	keepStaging = true
	digest := sha256.Sum256(contents)
	artifactPath, err := filepath.Rel(resolvedRoot, finalRoot)
	if err != nil || artifactPath == ".." || strings.HasPrefix(artifactPath, ".."+string(filepath.Separator)) {
		return models.GroupRevision{}, fs.ErrInvalid
	}
	revision := models.GroupRevision{
		ID: revisionID, ArtifactDigest: stringPointer(hex.EncodeToString(digest[:])),
		ArtifactPath: stringPointer(artifactPath),
	}
	_ = frontends
	return revision, nil
}

func (store GroupReleaseArtifacts) DiscardArtifact(_ context.Context, revision models.GroupRevision) error {
	if !validRevisionID(revision.ID) || revision.ArtifactPath == nil || *revision.ArtifactPath == "" {
		return errors.New(store.contract.InvalidContract)
	}
	root, err := filepath.EvalSymlinks(store.Root)
	if err != nil {
		return err
	}
	path := *revision.ArtifactPath
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
	return os.RemoveAll(resolved)
}

type artifactCounter struct {
	reader io.Reader
	read   int64
}

func (counter *artifactCounter) Read(buffer []byte) (int, error) {
	count, err := counter.reader.Read(buffer)
	counter.read += int64(count)
	return count, err
}

func extractFrontendArchive(root string, contents []byte, policy models.GroupReleasePolicy) ([]models.GroupRevisionFrontend, error) {
	invalid := func() ([]models.GroupRevisionFrontend, error) {
		return nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
	}
	tooLarge := func() ([]models.GroupRevisionFrontend, error) {
		return nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration), TooLarge: true}
	}
	compressed := &artifactCounter{reader: bytes.NewReader(contents)}
	decompressed, err := gzip.NewReader(compressed)
	if err != nil {
		return invalid()
	}
	limited := io.LimitReader(decompressed, policy.UncompressedArtifactLimitBytes+1)
	decompressedCounter := &artifactCounter{reader: limited}
	tarReader := tar.NewReader(decompressedCounter)
	entries := 0
	var uncompressedBytes int64
	seen := make(map[string]byte)
	frontendFiles := make(map[string]map[string]string)
	frontendIDs := make(map[string]struct{})
	folderMode := os.FileMode(policy.ArtifactDirectoryMode)
	fileMode := os.FileMode(policy.ArtifactFileMode)
	for {
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = decompressed.Close()
			if decompressedCounter.read > policy.UncompressedArtifactLimitBytes {
				return tooLarge()
			}
			return invalid()
		}
		entries++
		if entries > policy.ArtifactEntryLimit {
			_ = decompressed.Close()
			return tooLarge()
		}
		name, frontendID, safe := normalizedFrontendPath(header.Name, header.Typeflag, policy)
		if !safe {
			_ = decompressed.Close()
			return invalid()
		}
		folded := cases.Fold().String(name)
		if _, exists := seen[folded]; exists {
			_ = decompressed.Close()
			return invalid()
		}
		kind := header.Typeflag
		seen[folded] = kind
		frontendIDs[frontendID] = struct{}{}
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if !withinDirectory(root, absolute) {
			_ = decompressed.Close()
			return invalid()
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				_ = decompressed.Close()
				return invalid()
			}
			if err := os.MkdirAll(absolute, folderMode); err != nil {
				_ = decompressed.Close()
				return nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > policy.UncompressedArtifactLimitBytes-uncompressedBytes {
				_ = decompressed.Close()
				return invalid()
			}
			if err := os.MkdirAll(filepath.Dir(absolute), folderMode); err != nil {
				_ = decompressed.Close()
				return nil, err
			}
			file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
			if err != nil {
				_ = decompressed.Close()
				return invalid()
			}
			hash := sha256.New()
			written, copyErr := io.CopyN(io.MultiWriter(file, hash), tarReader, header.Size)
			syncErr := file.Sync()
			closeErr := file.Close()
			if copyErr != nil || written != header.Size || syncErr != nil || closeErr != nil {
				_ = decompressed.Close()
				return invalid()
			}
			uncompressedBytes += written
			relative := strings.TrimPrefix(name, "frontends/"+frontendID+"/")
			if frontendFiles[frontendID] == nil {
				frontendFiles[frontendID] = make(map[string]string)
			}
			frontendFiles[frontendID][relative] = hex.EncodeToString(hash.Sum(nil))
		default:
			_ = decompressed.Close()
			return invalid()
		}
	}
	if _, err := io.Copy(io.Discard, decompressedCounter); err != nil {
		_ = decompressed.Close()
		if decompressedCounter.read > policy.UncompressedArtifactLimitBytes {
			return tooLarge()
		}
		return invalid()
	}
	if decompressedCounter.read > policy.UncompressedArtifactLimitBytes {
		_ = decompressed.Close()
		return tooLarge()
	}
	if err := decompressed.Close(); err != nil {
		return invalid()
	}
	if len(frontendIDs) == 0 {
		return invalid()
	}
	if decompressedCounter.read > 0 && compressed.read == 0 {
		return invalid()
	}
	if compressed.read > 0 && float64(decompressedCounter.read)/float64(compressed.read) > float64(policy.CompressionRatioLimit) {
		return tooLarge()
	}
	ids := make([]string, 0, len(frontendIDs))
	for id := range frontendIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	frontends := make([]models.GroupRevisionFrontend, 0, len(ids))
	for _, id := range ids {
		paths := make([]string, 0, len(frontendFiles[id]))
		for relative := range frontendFiles[id] {
			paths = append(paths, relative)
		}
		sort.Strings(paths)
		hash := sha256.New()
		for _, relative := range paths {
			_, _ = io.WriteString(hash, relative)
			_, _ = hash.Write([]byte{0})
			fileDigest, _ := hex.DecodeString(frontendFiles[id][relative])
			_, _ = hash.Write(fileDigest)
		}
		frontends = append(frontends, models.GroupRevisionFrontend{ID: id, Digest: hex.EncodeToString(hash.Sum(nil)), Files: len(paths)})
	}
	return frontends, nil
}

func normalizedFrontendPath(raw string, typeflag byte, policy models.GroupReleasePolicy) (string, string, bool) {
	if raw == "" || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\\\x00") || filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") {
		return "", "", false
	}
	if typeflag == tar.TypeDir {
		raw = strings.TrimSuffix(raw, "/")
	}
	normalized := norm.NFC.String(raw)
	if len(normalized) > policy.ArtifactPathByteLimit {
		return "", "", false
	}
	segments := strings.Split(normalized, "/")
	if len(segments) < 2 || len(segments) > policy.ArtifactPathDepthLimit || segments[0] != "frontends" || segments[1] == "" || (typeflag != tar.TypeDir && len(segments) < 3) {
		return "", "", false
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", "", false
		}
		for _, character := range segment {
			if unicode.IsControl(character) {
				return "", "", false
			}
		}
	}
	if len(segments) > 2 && segments[1] == "" {
		return "", "", false
	}
	if strings.Contains(segments[0], ":") {
		return "", "", false
	}
	return strings.Join(segments, "/"), segments[1], true
}

func stringPointer(value string) *string {
	return &value
}

func validRevisionID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func withinDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
