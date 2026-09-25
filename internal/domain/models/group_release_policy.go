package models

import "time"

type GroupReleasePolicy struct {
	OperationKind                 string
	ScopePrefix                   string
	ScopeSuffix                   string
	AuditAction                   string
	SuccessResult                 string
	FailureResult                 string
	PendingState                  string
	RunningState                  string
	SucceededState                string
	FailedState                   string
	JournalPendingState           string
	JournalCompleteState          string
	JournalFailedState            string
	FragmentSeparator             string
	IdempotencyWindow             time.Duration
	SystemGroupID                 string
	SystemGroupKind               string
	InvalidConfiguration          string
	MetadataPart                  string
	CaddyfilePart                 string
	ArtifactPart                  string
	MultipartContentType          string
	RequestLimitBytes             int64
	InvalidRequestCode            string
	RevisionConflictCode          string
	IdempotencyConflictCode       string
	CaddyAdaptFailedCode          string
	ArtifactInvalidCode           string
	ArtifactTooLargeCode          string
	ActivationFailedCode          string
	MetadataIdempotencyKeyField   string
	MetadataExpectedRevisionField string
	RevisionIDPattern             string
}
