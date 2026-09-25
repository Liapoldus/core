package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupRevisionReader struct {
	Root string
}

func (reader GroupRevisionReader) Read(_ context.Context, revision models.GroupRevision) (models.GroupRevisionDetail, error) {
	if revision.ArtifactPath != nil {
		return models.GroupRevisionDetail{}, fs.ErrInvalid
	}
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
	return models.GroupRevisionDetail{Revision: revision, Caddyfile: string(contents), Frontends: []models.GroupRevisionFrontend{}}, nil
}
