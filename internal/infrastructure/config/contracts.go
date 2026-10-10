// Package config contains configuration compiler infrastructure adapters.
package config

import (
	"errors"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

type ServiceKeyWords struct {
	RolePlatformAdmin string `yaml:"rolePlatformAdmin"`
	KeyBytes          int    `yaml:"keyBytes"`
	HashCost          int    `yaml:"hashCost"`
}

type RuntimeWords struct {
	ServiceKey ServiceKeyWords `yaml:"serviceKey"`
	Outputs    struct {
		Text string `yaml:"text"`
		JSON string `yaml:"json"`
	} `yaml:"outputs"`
	Codes struct {
		ConfigNotFound string `yaml:"configNotFound"`
		ConfigInvalid  string `yaml:"configInvalid"`
	} `yaml:"codes"`
	Exits struct {
		OK          int `yaml:"ok"`
		Internal    int `yaml:"internal"`
		Arguments   int `yaml:"arguments"`
		Validation  int `yaml:"validation"`
		Conflict    int `yaml:"conflict"`
		Unavailable int `yaml:"unavailable"`
	} `yaml:"exits"`
	JSON struct {
		OK      string `yaml:"ok"`
		Problem string `yaml:"problem"`
		Code    string `yaml:"code"`
		Detail  string `yaml:"detail"`
		Token   string `yaml:"token"`
	} `yaml:"json"`
	Text struct {
		OK string `yaml:"ok"`
	} `yaml:"text"`
	Diagnostics struct {
		ConfigNotFound string `yaml:"configNotFound"`
		ConfigInvalid  string `yaml:"configInvalid"`
		OutputInvalid  string `yaml:"outputInvalid"`
	} `yaml:"diagnostics"`
}

func LoadRuntime() (RuntimeWords, error) { return runtimeContractDefinitions(), nil }

type ManagementWords struct {
	ServiceKeys struct {
		NameMinLength int `yaml:"nameMinLength"`
		NameMaxLength int `yaml:"nameMaxLength"`
		CreatedStatus int `yaml:"createdStatus"`
	} `yaml:"serviceKeys"`
	Codes struct {
		BearerRequired         string `yaml:"bearerRequired"`
		ManagementUnavailable  string `yaml:"managementUnavailable"`
		IdempotencyConflict    string `yaml:"idempotencyConflict"`
		PluginConfigInvalid    string `yaml:"pluginConfigInvalid"`
		PluginRevisionConflict string `yaml:"pluginRevisionConflict"`
		PluginLinkConflict     string `yaml:"pluginLinkConflict"`
		PluginLinkInvalid      string `yaml:"pluginLinkInvalid"`
		TargetLost             string `yaml:"targetLost"`
		ActivationFailed       string `yaml:"activationFailed"`
		PluginUnavailable      string `yaml:"pluginUnavailable"`
		PluginNotFound         string `yaml:"pluginNotFound"`
		InvalidRequest         string `yaml:"invalidRequest"`
		ArtifactTooLarge       string `yaml:"artifactTooLarge"`
		OperationNotFound      string `yaml:"operationNotFound"`
	} `yaml:"codes"`
	Paths struct {
		Healthz                string `yaml:"healthz"`
		Status                 string `yaml:"status"`
		Config                 string `yaml:"config"`
		ConfigValidate         string `yaml:"configValidate"`
		ConfigReload           string `yaml:"configReload"`
		Reload                 string `yaml:"reload"`
		Listeners              string `yaml:"listeners"`
		Upstreams              string `yaml:"upstreams"`
		Plugins                string `yaml:"plugins"`
		PluginSettingsSuffix   string `yaml:"pluginSettingsSuffix"`
		PluginRollbackSuffix   string `yaml:"pluginRollbackSuffix"`
		AdminSurfaces          string `yaml:"adminSurfaces"`
		AdminPages             string `yaml:"adminPages"`
		AdminActions           string `yaml:"adminActions"`
		AdminQueryAction       string `yaml:"adminQueryAction"`
		Logs                   string `yaml:"logs"`
		TLS                    string `yaml:"tls"`
		Renew                  string `yaml:"renew"`
		Revoke                 string `yaml:"revoke"`
		Operations             string `yaml:"operations"`
		Audit                  string `yaml:"audit"`
		ServiceKeys            string `yaml:"serviceKeys"`
		PluginLinks            string `yaml:"pluginLinks"`
		PluginLinkTargetSuffix string `yaml:"pluginLinkTargetSuffix"`
		PluginIDSeparator      string `yaml:"pluginIDSeparator"`
		ConfigBundlePlan       string `yaml:"configBundlePlan"`
		ConfigBundleApply      string `yaml:"configBundleApply"`
	} `yaml:"paths"`
	OperationKinds struct {
		PluginSettingsApply    string `yaml:"pluginSettingsApply"`
		PluginSettingsRollback string `yaml:"pluginSettingsRollback"`
		PluginLinkCreate       string `yaml:"pluginLinkCreate"`
		PluginLinkReplace      string `yaml:"pluginLinkReplace"`
		PluginLinkDelete       string `yaml:"pluginLinkDelete"`
	} `yaml:"operationKinds"`
	Methods struct {
		Get    string `yaml:"get"`
		Post   string `yaml:"post"`
		Put    string `yaml:"put"`
		Delete string `yaml:"delete"`
	} `yaml:"methods"`
	JSON struct {
		RequestID               string `yaml:"requestId"`
		OperationID             string `yaml:"operationId"`
		State                   string `yaml:"state"`
		Items                   string `yaml:"items"`
		NextCursor              string `yaml:"nextCursor"`
		YAML                    string `yaml:"yaml"`
		Digest                  string `yaml:"digest"`
		Valid                   string `yaml:"valid"`
		IdempotencyKey          string `yaml:"idempotencyKey"`
		ErrorCode               string `yaml:"errorCode"`
		ResourceID              string `yaml:"resourceId"`
		ExpectedRevision        string `yaml:"expectedRevision"`
		ID                      string `yaml:"id"`
		ArtifactDigest          string `yaml:"artifactDigest"`
		Frontends               string `yaml:"frontends"`
		Files                   string `yaml:"files"`
		ReplyTo                 string `yaml:"replyTo"`
		Status                  string `yaml:"status"`
		Slug                    string `yaml:"slug"`
		Route                   string `yaml:"route"`
		Root                    string `yaml:"root"`
		CurrentRevision         string `yaml:"currentRevision"`
		PreviousRevision        string `yaml:"previousRevision"`
		CreatedAt               string `yaml:"createdAt"`
		ExpiresAt               string `yaml:"expiresAt"`
		RevokedAt               string `yaml:"revokedAt"`
		UpdatedAt               string `yaml:"updatedAt"`
		StartedAt               string `yaml:"startedAt"`
		FinishedAt              string `yaml:"finishedAt"`
		Result                  string `yaml:"result"`
		Problem                 string `yaml:"problem"`
		Timestamp               string `yaml:"timestamp"`
		Actor                   string `yaml:"actor"`
		Action                  string `yaml:"action"`
		Resource                string `yaml:"resource"`
		DigestBefore            string `yaml:"digestBefore"`
		DigestAfter             string `yaml:"digestAfter"`
		Name                    string `yaml:"name"`
		Role                    string `yaml:"role"`
		Token                   string `yaml:"token"`
		Type                    string `yaml:"type"`
		Address                 string `yaml:"address"`
		ActiveConnections       string `yaml:"activeConnections"`
		Healthy                 string `yaml:"healthy"`
		Capabilities            string `yaml:"capabilities"`
		Limits                  string `yaml:"limits"`
		Health                  string `yaml:"health"`
		Profile                 string `yaml:"profile"`
		Domain                  string `yaml:"domain"`
		Serial                  string `yaml:"serial"`
		NotAfter                string `yaml:"notAfter"`
		Validity                string `yaml:"validity"`
		Diagnostics             string `yaml:"diagnostics"`
		Limit                   string `yaml:"limit"`
		Cursor                  string `yaml:"cursor"`
		Kind                    string `yaml:"kind"`
		Active                  string `yaml:"active"`
		Drift                   string `yaml:"drift"`
		RuntimeDigest           string `yaml:"runtimeDigest"`
		CompositionDigest       string `yaml:"compositionDigest"`
		DataPlaneReadiness      string `yaml:"dataPlaneReadiness"`
		Reason                  string `yaml:"reason"`
		InstanceID              string `yaml:"instanceId"`
		Capability              string `yaml:"capability"`
		AllowedNames            string `yaml:"allowedNames"`
		Revision                string `yaml:"revision"`
		Config                  string `yaml:"config"`
		CallerInstanceID        string `yaml:"callerInstanceId"`
		TargetInstanceID        string `yaml:"targetInstanceId"`
		Rules                   string `yaml:"rules"`
		PlacementRule           string `yaml:"placementRule"`
		Carrier                 string `yaml:"carrier"`
		Weight                  string `yaml:"weight"`
		RequiredContracts       string `yaml:"requiredContracts"`
		ContractID              string `yaml:"contractId"`
		MinimumVersion          string `yaml:"minimumVersion"`
		MaximumVersionExclusive string `yaml:"maximumVersionExclusive"`
	} `yaml:"json"`
	Headers struct {
		IfMatch            string `yaml:"ifMatch"`
		AdminSurfaceDigest string `yaml:"adminSurfaceDigest"`
		ContentType        string `yaml:"contentType"`
		ETag               string `yaml:"etag"`
		RequestID          string `yaml:"requestId"`
		Location           string `yaml:"location"`
		RetryAfter         string `yaml:"retryAfter"`
	} `yaml:"headers"`
	ContentTypes struct {
		YAML    string `yaml:"yaml"`
		JSON    string `yaml:"json"`
		Problem string `yaml:"problem"`
		Text    string `yaml:"text"`
	} `yaml:"contentTypes"`
	Statuses struct {
		OK                    string `yaml:"ok"`
		Ready                 string `yaml:"ready"`
		Draining              string `yaml:"draining"`
		Failed                string `yaml:"failed"`
		Invalid               string `yaml:"invalid"`
		Publishing            string `yaml:"publishing"`
		Healthy               string `yaml:"healthy"`
		Degraded              string `yaml:"degraded"`
		Unavailable           string `yaml:"unavailable"`
		Unhealthy             string `yaml:"unhealthy"`
		Stopped               string `yaml:"stopped"`
		Renewing              string `yaml:"renewing"`
		Pending               string `yaml:"pending"`
		Running               string `yaml:"running"`
		Completed             string `yaml:"completed"`
		Succeeded             string `yaml:"succeeded"`
		Empty                 string `yaml:"empty"`
		NotReady              string `yaml:"notReady"`
		SystemReleaseRequired string `yaml:"systemReleaseRequired"`
		RecoveryRequired      string `yaml:"recoveryRequired"`
	} `yaml:"statuses"`
	Idempotency struct {
		LimitDefault int    `yaml:"limitDefault"`
		LimitMax     int    `yaml:"limitMax"`
		LimitMin     int    `yaml:"limitMin"`
		KeyMin       int    `yaml:"keyMin"`
		KeyMax       int    `yaml:"keyMax"`
		Window       string `yaml:"window"`
		Key          string `yaml:"key"`
		ReplyTo      string `yaml:"replyTo"`
	} `yaml:"idempotency"`
	Pagination struct {
		LimitDefault int `yaml:"limitDefault"`
		LimitMax     int `yaml:"limitMax"`
		LimitMin     int `yaml:"limitMin"`
	} `yaml:"pagination"`
	OperationState struct {
		Done     string `yaml:"done"`
		Accepted string `yaml:"accepted"`
	} `yaml:"operationState"`
	Diagnostics struct {
		OperationNotFound string `yaml:"operationNotFound"`
	} `yaml:"diagnostics"`
}

type PluginConfigurationWords struct {
	SchemaVersion       int64 `yaml:"schemaVersion"`
	MaximumPayloadBytes int   `yaml:"maximumPayloadBytes"`
	Slots               struct {
		Active   string `yaml:"active"`
		Previous string `yaml:"previous"`
		Staging  string `yaml:"staging"`
	} `yaml:"slots"`
	OperationStates struct {
		Pending string `yaml:"pending"`
		Running string `yaml:"running"`
	} `yaml:"operationStates"`
	Diagnostics struct {
		InvalidContract string `yaml:"invalidContract"`
		MigrationFailed string `yaml:"migrationFailed"`
	} `yaml:"diagnostics"`
}

func LoadPluginConfiguration() (PluginConfigurationWords, error) {
	definition := storage.ConfigurationDefinitions()
	var loaded PluginConfigurationWords
	loaded.SchemaVersion = definition.SchemaVersion
	loaded.MaximumPayloadBytes = definition.MaximumPayloadSize
	loaded.Slots.Active = definition.Slots.Active
	loaded.Slots.Previous = definition.Slots.Previous
	loaded.Slots.Staging = definition.Slots.Staging
	loaded.OperationStates.Pending = definition.OperationStates.Pending
	loaded.OperationStates.Running = definition.OperationStates.Running
	loaded.Diagnostics.InvalidContract = definition.Diagnostics.InvalidContract
	loaded.Diagnostics.MigrationFailed = definition.Diagnostics.MigrationFailed
	if loaded.SchemaVersion < 1 || loaded.MaximumPayloadBytes < 1 || loaded.Slots.Active == "" || loaded.Slots.Previous == "" || loaded.Slots.Staging == "" ||
		loaded.OperationStates.Pending == "" || loaded.OperationStates.Running == "" || loaded.Diagnostics.InvalidContract == "" || loaded.Diagnostics.MigrationFailed == "" {
		return PluginConfigurationWords{}, errors.New(loaded.Diagnostics.InvalidContract)
	}
	return loaded, nil
}

func LoadManagement() (ManagementWords, error) {
	return managementDefinitions(), nil
}

type AuditWords struct {
	Audit struct {
		RetentionDays int `yaml:"retentionDays"`
		Actors        struct {
			StaticToken string `yaml:"staticToken"`
		} `yaml:"actors"`
		Actions struct {
			ServiceKeyCreate          string `yaml:"serviceKeyCreate"`
			PluginSettingsCandidate   string `yaml:"pluginSettingsCandidate"`
			PluginSettingsApply       string `yaml:"pluginSettingsApply"`
			PluginSettingsApplyFailed string `yaml:"pluginSettingsApplyFailed"`
			PluginSettingsRollback    string `yaml:"pluginSettingsRollback"`
			PluginAdminAction         string `yaml:"pluginAdminAction"`
			PluginLinkCreate          string `yaml:"pluginLinkCreate"`
			PluginLinkReplace         string `yaml:"pluginLinkReplace"`
			PluginLinkDelete          string `yaml:"pluginLinkDelete"`
		} `yaml:"actions"`
		Resources struct {
			ServiceKeys string `yaml:"serviceKeys"`
		} `yaml:"resources"`
		Results struct {
			Succeeded string `yaml:"succeeded"`
			Failed    string `yaml:"failed"`
		} `yaml:"results"`
		StorageUnavailable struct {
			Code   string `yaml:"code"`
			Detail string `yaml:"detail"`
		} `yaml:"storageUnavailable"`
		InvalidLimit string `yaml:"invalidLimit"`
	} `yaml:"audit"`
}

func LoadAudit() (AuditWords, error) {
	return auditDefinitions(), nil
}

type ErrorCatalog struct {
	codes map[string]models.Problem
}

func (catalog ErrorCatalog) Lookup(code string) (models.Problem, bool) {
	problem, exists := catalog.codes[code]
	return problem, exists
}

func LoadErrorCatalog() (ErrorCatalog, error) {
	loaded := ErrorDefinitions()
	catalog := ErrorCatalog{codes: make(map[string]models.Problem, len(loaded.Errors))}
	for _, entry := range loaded.Errors {
		catalog.codes[entry.Code] = models.Problem{
			Type:   "https://liapoldus.dev/problems/" + entry.Code,
			Title:  entry.Title,
			Status: entry.Status,
			Code:   entry.Code,
			Detail: entry.Detail,
		}
	}
	return catalog, nil
}

func (catalog ErrorCatalog) Problem(code, detail, path string) models.Problem {
	problem, ok := catalog.codes[code]
	if !ok {
		return models.Problem{
			Type:   "https://liapoldus.dev/problems/internal_error",
			Title:  "Internal error",
			Status: 500,
			Code:   "internal_error",
			Detail: detail,
			Path:   path,
		}
	}
	problem.Detail = detail
	problem.Path = path
	return problem
}
