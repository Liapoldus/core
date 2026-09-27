package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
)

type pluginCookiePolicyInput struct {
	AllowedNames []string `json:"allowedNames"`
}

type groupReleaseMetadata struct {
	IdempotencyKey          string
	ExpectedCurrentRevision *string
}

func readGroupReleaseMultipart(reader *multipart.Reader, policy models.GroupReleasePolicy, management config.ManagementWords) (groupReleaseMetadata, []byte, []byte, error) {
	var result groupReleaseMetadata
	var metadataBytes, caddyfile, artifact []byte
	metadataSeen, caddyfileSeen := false, false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, nil, nil, err
		}
		contents, err := io.ReadAll(io.LimitReader(part, policy.RequestLimitBytes+1))
		_ = part.Close()
		if err != nil || int64(len(contents)) > policy.RequestLimitBytes {
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
		switch part.FormName() {
		case policy.MetadataPart:
			if metadataSeen {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if typeErr != nil || partType != policy.MetadataContentType {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			metadataSeen = true
			metadataBytes = contents
		case policy.CaddyfilePart:
			if caddyfileSeen || !utf8.Valid(contents) {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if typeErr != nil || partType != policy.CaddyfileContentType {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			caddyfileSeen = true
			caddyfile = contents
		case policy.ArtifactPart:
			if artifact != nil {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			fileName := part.FileName()
			if typeErr != nil || partType != policy.ArtifactContentType || !strings.HasSuffix(strings.ToLower(fileName), policy.ArtifactSuffix) || strings.ContainsAny(fileName, "/\\") {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
			}
			if int64(len(contents)) > policy.CompressedArtifactLimitBytes {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration), TooLarge: true}
			}
			artifact = contents
		default:
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
	}
	if !metadataSeen || !caddyfileSeen || len(caddyfile) == 0 {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(metadataBytes, &fields); err != nil || len(fields) != 2 {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	keyJSON, hasKey := fields[policy.MetadataIdempotencyKeyField]
	expectedJSON, hasExpected := fields[policy.MetadataExpectedRevisionField]
	if !hasKey || !hasExpected || json.Unmarshal(keyJSON, &result.IdempotencyKey) != nil || len(result.IdempotencyKey) < management.Idempotency.KeyMin || len(result.IdempotencyKey) > management.Idempotency.KeyChars || !ascii(result.IdempotencyKey) {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	var expected *string
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	if expected != nil {
		valid, err := regexp.MatchString(policy.RevisionIDPattern, *expected)
		if err != nil || !valid {
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
		result.ExpectedCurrentRevision = expected
	}
	return result, caddyfile, artifact, nil
}

func (server *Server) groupResponse(group models.Group) map[string]any {
	state := server.Management.Statuses.Ready
	if group.CurrentRevisionID == nil {
		state = server.Management.Statuses.Empty
	}
	return map[string]any{
		server.Management.JSON.ID:               group.ID,
		server.Management.JSON.Kind:             group.Kind,
		server.Management.JSON.Active:           group.Active,
		server.Management.JSON.CurrentRevision:  group.CurrentRevisionID,
		server.Management.JSON.PreviousRevision: group.PreviousRevisionID,
		server.Management.JSON.State:            state,
	}
}

func sliceValues(values any) []any {
	switch typed := values.(type) {
	case []any:
		return typed
	case []models.AuditRecord:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = typed[i]
		}
		return result
	default:
		return nil
	}
}

func ascii(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
