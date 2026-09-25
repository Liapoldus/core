package config

import (
	"errors"
	"time"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type GroupReleaseWords struct {
	OperationKind                  string `yaml:"operationKind"`
	ScopePrefix                    string `yaml:"scopePrefix"`
	ScopeSuffix                    string `yaml:"scopeSuffix"`
	AuditAction                    string `yaml:"auditAction"`
	SuccessResult                  string `yaml:"successResult"`
	FailureResult                  string `yaml:"failureResult"`
	PendingState                   string `yaml:"pendingState"`
	RunningState                   string `yaml:"runningState"`
	SucceededState                 string `yaml:"succeededState"`
	FailedState                    string `yaml:"failedState"`
	JournalPendingState            string `yaml:"journalPendingState"`
	JournalCompleteState           string `yaml:"journalCompleteState"`
	JournalFailedState             string `yaml:"journalFailedState"`
	FragmentSeparator              string `yaml:"fragmentSeparator"`
	IdempotencyWindow              string `yaml:"idempotencyWindow"`
	InvalidContract                string `yaml:"invalidContract"`
	SystemGroupKind                string `yaml:"systemGroupKind"`
	InvalidConfiguration           string `yaml:"invalidConfiguration"`
	MetadataPart                   string `yaml:"metadataPart"`
	CaddyfilePart                  string `yaml:"caddyfilePart"`
	ArtifactPart                   string `yaml:"artifactPart"`
	MultipartContentType           string `yaml:"multipartContentType"`
	RequestLimitBytes              int64  `yaml:"requestLimitBytes"`
	CompressedArtifactLimitBytes   int64  `yaml:"compressedArtifactLimitBytes"`
	UncompressedArtifactLimitBytes int64  `yaml:"uncompressedArtifactLimitBytes"`
	ArtifactEntryLimit             int    `yaml:"artifactEntryLimit"`
	CompressionRatioLimit          int64  `yaml:"compressionRatioLimit"`
	ArtifactPathByteLimit          int    `yaml:"artifactPathByteLimit"`
	ArtifactPathDepthLimit         int    `yaml:"artifactPathDepthLimit"`
	ArtifactSuffix                 string `yaml:"artifactSuffix"`
	MetadataContentType            string `yaml:"metadataContentType"`
	CaddyfileContentType           string `yaml:"caddyfileContentType"`
	ArtifactContentType            string `yaml:"artifactContentType"`
	ArtifactFileMode               uint32 `yaml:"artifactFileMode"`
	ArtifactDirectoryMode          uint32 `yaml:"artifactDirectoryMode"`
	InvalidRequestCode             string `yaml:"invalidRequestCode"`
	RevisionConflictCode           string `yaml:"revisionConflictCode"`
	IdempotencyConflictCode        string `yaml:"idempotencyConflictCode"`
	CaddyAdaptFailedCode           string `yaml:"caddyAdaptFailedCode"`
	ArtifactInvalidCode            string `yaml:"artifactInvalidCode"`
	ArtifactTooLargeCode           string `yaml:"artifactTooLargeCode"`
	ActivationFailedCode           string `yaml:"activationFailedCode"`
	MetadataIdempotencyKeyField    string `yaml:"metadataIdempotencyKeyField"`
	MetadataExpectedRevisionField  string `yaml:"metadataExpectedRevisionField"`
	RevisionIDPattern              string `yaml:"revisionIDPattern"`
}

func LoadGroupRelease() (models.GroupReleasePolicy, error) {
	contents, err := assets.Contract(assets.GroupReleaseWorkflow)
	if err != nil {
		return models.GroupReleasePolicy{}, err
	}
	var words GroupReleaseWords
	if err := yaml.Unmarshal(contents, &words); err != nil {
		return models.GroupReleasePolicy{}, err
	}
	if words.OperationKind == "" || words.ScopePrefix == "" || words.ScopeSuffix == "" || words.AuditAction == "" || words.SuccessResult == "" || words.FailureResult == "" || words.PendingState == "" || words.RunningState == "" || words.SucceededState == "" || words.FailedState == "" || words.JournalPendingState == "" || words.JournalCompleteState == "" || words.JournalFailedState == "" || words.FragmentSeparator == "" || words.IdempotencyWindow == "" || words.InvalidContract == "" || words.SystemGroupKind == "" || words.InvalidConfiguration == "" || words.MetadataPart == "" || words.CaddyfilePart == "" || words.ArtifactPart == "" || words.MultipartContentType == "" || words.RequestLimitBytes <= 0 || words.InvalidRequestCode == "" || words.RevisionConflictCode == "" || words.IdempotencyConflictCode == "" || words.CaddyAdaptFailedCode == "" || words.ArtifactInvalidCode == "" || words.ArtifactTooLargeCode == "" || words.ActivationFailedCode == "" || words.MetadataIdempotencyKeyField == "" || words.MetadataExpectedRevisionField == "" || words.RevisionIDPattern == "" || words.CompressedArtifactLimitBytes <= 0 || words.UncompressedArtifactLimitBytes <= 0 || words.ArtifactEntryLimit <= 0 || words.CompressionRatioLimit <= 0 || words.ArtifactPathByteLimit <= 0 || words.ArtifactPathDepthLimit <= 0 || words.ArtifactSuffix == "" || words.MetadataContentType == "" || words.CaddyfileContentType == "" || words.ArtifactContentType == "" || words.ArtifactFileMode == 0 || words.ArtifactDirectoryMode == 0 {
		return models.GroupReleasePolicy{}, errors.New(words.InvalidContract)
	}
	window, err := time.ParseDuration(words.IdempotencyWindow)
	if err != nil || window <= 0 {
		return models.GroupReleasePolicy{}, errors.New(words.InvalidContract)
	}
	sqlite, err := LoadSQLiteContract()
	if err != nil {
		return models.GroupReleasePolicy{}, err
	}
	return models.GroupReleasePolicy{
		OperationKind: words.OperationKind, ScopePrefix: words.ScopePrefix, ScopeSuffix: words.ScopeSuffix,
		AuditAction: words.AuditAction, SuccessResult: words.SuccessResult, FailureResult: words.FailureResult,
		PendingState: words.PendingState, RunningState: words.RunningState, SucceededState: words.SucceededState,
		FailedState: words.FailedState, JournalPendingState: words.JournalPendingState,
		JournalCompleteState: words.JournalCompleteState, JournalFailedState: words.JournalFailedState,
		FragmentSeparator: words.FragmentSeparator, IdempotencyWindow: window,
		SystemGroupID: sqlite.SystemGroupID, SystemGroupKind: words.SystemGroupKind,
		InvalidConfiguration: words.InvalidConfiguration,
		MetadataPart:         words.MetadataPart, CaddyfilePart: words.CaddyfilePart, ArtifactPart: words.ArtifactPart,
		MultipartContentType: words.MultipartContentType, RequestLimitBytes: words.RequestLimitBytes,
		CompressedArtifactLimitBytes: words.CompressedArtifactLimitBytes, UncompressedArtifactLimitBytes: words.UncompressedArtifactLimitBytes,
		ArtifactEntryLimit: words.ArtifactEntryLimit, CompressionRatioLimit: words.CompressionRatioLimit,
		ArtifactPathByteLimit: words.ArtifactPathByteLimit, ArtifactPathDepthLimit: words.ArtifactPathDepthLimit,
		ArtifactSuffix: words.ArtifactSuffix, MetadataContentType: words.MetadataContentType,
		CaddyfileContentType: words.CaddyfileContentType, ArtifactContentType: words.ArtifactContentType,
		ArtifactFileMode: words.ArtifactFileMode, ArtifactDirectoryMode: words.ArtifactDirectoryMode,
		InvalidRequestCode: words.InvalidRequestCode, RevisionConflictCode: words.RevisionConflictCode,
		IdempotencyConflictCode: words.IdempotencyConflictCode, CaddyAdaptFailedCode: words.CaddyAdaptFailedCode,
		ArtifactInvalidCode: words.ArtifactInvalidCode, ArtifactTooLargeCode: words.ArtifactTooLargeCode,
		ActivationFailedCode:          words.ActivationFailedCode,
		MetadataIdempotencyKeyField:   words.MetadataIdempotencyKeyField,
		MetadataExpectedRevisionField: words.MetadataExpectedRevisionField,
		RevisionIDPattern:             words.RevisionIDPattern,
	}, nil
}
