package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type GroupRevisionReader struct {
	Root string
}

func (reader GroupRevisionReader) Read(_ context.Context, revision models.GroupRevision) (models.GroupRevisionDetail, error) {
	root, err := filepath.EvalSymlinks(reader.Root)
	if err != nil {
		return models.GroupRevisionDetail{}, err
	}
	path := revision.CaddyfilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(reader.Root, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return models.GroupRevisionDetail{}, err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return models.GroupRevisionDetail{}, fs.ErrInvalid
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		return models.GroupRevisionDetail{}, err
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != revision.CaddyfileDigest {
		return models.GroupRevisionDetail{}, fs.ErrInvalid
	}
	frontends := []models.GroupRevisionFrontend{}
	if revision.ArtifactPath != nil {
		frontends, err = readFrontendManifest(root, reader.Root, *revision.ArtifactPath, revision.ArtifactDigest)
		if err != nil {
			return models.GroupRevisionDetail{}, err
		}
	}
	return models.GroupRevisionDetail{Revision: revision, Caddyfile: string(contents), Frontends: frontends}, nil
}

func readFrontendManifest(root, base, artifactPath string, expectedDigest *string) ([]models.GroupRevisionFrontend, error) {
	path := artifactPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	relativeRoot, err := filepath.Rel(root, resolved)
	if err != nil || relativeRoot == ".." || strings.HasPrefix(relativeRoot, ".."+string(filepath.Separator)) {
		return nil, fs.ErrInvalid
	}
	if expectedDigest == nil || len(*expectedDigest) != sha256.Size*2 {
		return nil, fs.ErrInvalid
	}
	contractContents, err := assets.Contract(assets.GroupReleaseArtifacts)
	if err != nil {
		return nil, err
	}
	var contract groupReleaseArtifactContract
	if err := yaml.Unmarshal(contractContents, &contract); err != nil || contract.SourceArchiveName == "" {
		return nil, fs.ErrInvalid
	}
	archive, err := os.ReadFile(filepath.Join(resolved, contract.SourceArchiveName))
	if err != nil {
		return nil, err
	}
	archiveDigest := sha256.Sum256(archive)
	if hex.EncodeToString(archiveDigest[:]) != *expectedDigest {
		return nil, fs.ErrInvalid
	}
	files := make(map[string]map[string]string)
	err = filepath.WalkDir(resolved, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Base(path) == contract.SourceArchiveName && filepath.Dir(path) == resolved {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fs.ErrInvalid
		}
		relative, err := filepath.Rel(resolved, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fs.ErrInvalid
		}
		segments := strings.Split(filepath.ToSlash(relative), "/")
		if len(segments) < 3 || segments[0] != "frontends" || segments[1] == "" {
			return fs.ErrInvalid
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		if files[segments[1]] == nil {
			files[segments[1]] = make(map[string]string)
		}
		files[segments[1]][strings.Join(segments[2:], "/")] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]models.GroupRevisionFrontend, 0, len(ids))
	for _, id := range ids {
		paths := make([]string, 0, len(files[id]))
		for filePath := range files[id] {
			paths = append(paths, filePath)
		}
		sort.Strings(paths)
		hash := sha256.New()
		for _, filePath := range paths {
			_, _ = io.WriteString(hash, filePath)
			_, _ = hash.Write([]byte{0})
			fileDigest, _ := hex.DecodeString(files[id][filePath])
			_, _ = hash.Write(fileDigest)
		}
		result = append(result, models.GroupRevisionFrontend{ID: id, Digest: hex.EncodeToString(hash.Sum(nil)), Files: len(paths)})
	}
	return result, nil
}
