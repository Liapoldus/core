package config

// Definitions are owned by code; generated contracts are build artifacts.

func auditDefinitions() AuditWords {
	return AuditWords{Audit: struct {
		RetentionDays int "yaml:\"retentionDays\""
		Actors        struct {
			StaticToken string "yaml:\"staticToken\""
		} "yaml:\"actors\""
		Actions struct {
			ServiceKeyCreate           string "yaml:\"serviceKeyCreate\""
			ServiceSettingsCandidate   string "yaml:\"serviceSettingsCandidate\""
			ServiceSettingsApply       string "yaml:\"serviceSettingsApply\""
			ServiceSettingsApplyFailed string "yaml:\"serviceSettingsApplyFailed\""
			ServiceSettingsRollback    string "yaml:\"serviceSettingsRollback\""
			ServiceAdminAction         string "yaml:\"serviceAdminAction\""
			ServiceLinkCreate          string "yaml:\"serviceLinkCreate\""
			ServiceLinkReplace         string "yaml:\"serviceLinkReplace\""
			ServiceLinkDelete          string "yaml:\"serviceLinkDelete\""
		} "yaml:\"actions\""
		Resources struct {
			ServiceKeys string "yaml:\"serviceKeys\""
		} "yaml:\"resources\""
		Results struct {
			Succeeded string "yaml:\"succeeded\""
			Failed    string "yaml:\"failed\""
		} "yaml:\"results\""
		StorageUnavailable struct {
			Code   string "yaml:\"code\""
			Detail string "yaml:\"detail\""
		} "yaml:\"storageUnavailable\""
		InvalidLimit string "yaml:\"invalidLimit\""
	}{RetentionDays: 90,
		Actors: struct {
			StaticToken string "yaml:\"staticToken\""
		}{StaticToken: "static-token"},
		Actions: struct {
			ServiceKeyCreate           string "yaml:\"serviceKeyCreate\""
			ServiceSettingsCandidate   string "yaml:\"serviceSettingsCandidate\""
			ServiceSettingsApply       string "yaml:\"serviceSettingsApply\""
			ServiceSettingsApplyFailed string "yaml:\"serviceSettingsApplyFailed\""
			ServiceSettingsRollback    string "yaml:\"serviceSettingsRollback\""
			ServiceAdminAction         string "yaml:\"serviceAdminAction\""
			ServiceLinkCreate          string "yaml:\"serviceLinkCreate\""
			ServiceLinkReplace         string "yaml:\"serviceLinkReplace\""
			ServiceLinkDelete          string "yaml:\"serviceLinkDelete\""
		}{ServiceKeyCreate: "service_key.create",
			ServiceSettingsCandidate:   "service_settings.candidate",
			ServiceSettingsApply:       "service_settings.apply",
			ServiceSettingsApplyFailed: "service_settings.apply_failed",
			ServiceSettingsRollback:    "service_settings.rollback",
			ServiceAdminAction:         "service_admin.action",
			ServiceLinkCreate:          "service_link.create",
			ServiceLinkReplace:         "service_link.replace",
			ServiceLinkDelete:          "service_link.delete"},
		Resources: struct {
			ServiceKeys string "yaml:\"serviceKeys\""
		}{ServiceKeys: "service-keys"},
		Results: struct {
			Succeeded string "yaml:\"succeeded\""
			Failed    string "yaml:\"failed\""
		}{Succeeded: "succeeded",
			Failed: "failed"},
		StorageUnavailable: struct {
			Code   string "yaml:\"code\""
			Detail string "yaml:\"detail\""
		}{Code: "audit_unavailable",
			Detail: "audit storage is unavailable"},
		InvalidLimit: "audit page limit is invalid"}}
}

func runtimeContractDefinitions() RuntimeWords {
	return RuntimeWords{ServiceKey: ServiceKeyWords{RolePlatformAdmin: "platform-admin",
		KeyBytes: 32,
		HashCost: 12},
		Outputs: struct {
			Text string "yaml:\"text\""
			JSON string "yaml:\"json\""
		}{Text: "text",
			JSON: "json"},
		Codes: struct {
			ConfigNotFound string "yaml:\"configNotFound\""
			ConfigInvalid  string "yaml:\"configInvalid\""
		}{ConfigNotFound: "config_not_found",
			ConfigInvalid: "config_invalid"},
		Exits: struct {
			OK          int "yaml:\"ok\""
			Internal    int "yaml:\"internal\""
			Arguments   int "yaml:\"arguments\""
			Validation  int "yaml:\"validation\""
			Conflict    int "yaml:\"conflict\""
			Unavailable int "yaml:\"unavailable\""
		}{OK: 0,
			Internal:    1,
			Arguments:   2,
			Validation:  3,
			Conflict:    4,
			Unavailable: 7},
		JSON: struct {
			OK      string "yaml:\"ok\""
			Problem string "yaml:\"problem\""
			Code    string "yaml:\"code\""
			Detail  string "yaml:\"detail\""
			Token   string "yaml:\"token\""
		}{OK: "ok",
			Problem: "problem",
			Code:    "code",
			Detail:  "detail",
			Token:   "token"},
		Text: struct {
			OK string "yaml:\"ok\""
		}{OK: "ok"},
		Diagnostics: struct {
			ConfigNotFound string "yaml:\"configNotFound\""
			ConfigInvalid  string "yaml:\"configInvalid\""
			OutputInvalid  string "yaml:\"outputInvalid\""
		}{ConfigNotFound: "Файл конфигурации не найден.",
			ConfigInvalid: "Конфигурация не прошла проверку.",
			OutputInvalid: "--output должен иметь значение text или json"}}
}

func grantDefinitions() ServiceSecretGrantPolicy {
	return ServiceSecretGrantPolicy{SchemaVersion: 1,
		MaximumOutstanding: 256}
}

func inventoryDefinitions() ServiceInventoryContract {
	return ServiceInventoryContract{SelectInstances: "SELECT instances.id, instances.state, active.generation, instances.manifest_json, active.raw_json FROM service_instances AS instances JOIN service_config_generations AS active ON active.instance_id = instances.id AND active.slot = 'active' ORDER BY instances.id",
		ValidStates: []string{"configured",
			"ready",
			"degraded",
			"failed"},
		JSON: struct {
			ID       string "yaml:\"id\""
			State    string "yaml:\"state\""
			Revision string "yaml:\"revision\""
		}{ID: "id",
			State:    "state",
			Revision: "revision"},
		Diagnostics: struct {
			InvalidContract string "yaml:\"invalidContract\""
			InvalidRecord   string "yaml:\"invalidRecord\""
			InvalidManifest string "yaml:\"invalidManifest\""
		}{InvalidContract: "service inventory contract is invalid",
			InvalidRecord:   "service instance record is invalid",
			InvalidManifest: "service instance manifest is invalid"}}
}

func managementDefinitions() ManagementWords {
	return ManagementWords{ServiceKeys: struct {
		NameMinLength int "yaml:\"nameMinLength\""
		NameMaxLength int "yaml:\"nameMaxLength\""
		CreatedStatus int "yaml:\"createdStatus\""
	}{NameMinLength: 1,
		NameMaxLength: 80,
		CreatedStatus: 201},
		Codes: struct {
			BearerRequired          string "yaml:\"bearerRequired\""
			ManagementUnavailable   string "yaml:\"managementUnavailable\""
			IdempotencyConflict     string "yaml:\"idempotencyConflict\""
			ServiceConfigInvalid    string "yaml:\"serviceConfigInvalid\""
			ServiceRevisionConflict string "yaml:\"serviceRevisionConflict\""
			ServiceLinkConflict     string "yaml:\"serviceLinkConflict\""
			ServiceLinkInvalid      string "yaml:\"serviceLinkInvalid\""
			TargetLost              string "yaml:\"targetLost\""
			ActivationFailed        string "yaml:\"activationFailed\""
			ServiceUnavailable      string "yaml:\"serviceUnavailable\""
			ServiceNotFound         string "yaml:\"serviceNotFound\""
			InvalidRequest          string "yaml:\"invalidRequest\""
			ArtifactTooLarge        string "yaml:\"artifactTooLarge\""
			OperationNotFound       string "yaml:\"operationNotFound\""
		}{BearerRequired: "management_bearer_required",
			ManagementUnavailable:   "management_unavailable",
			IdempotencyConflict:     "idempotency_conflict",
			ServiceConfigInvalid:    "service_config_invalid",
			ServiceRevisionConflict: "service_revision_conflict",
			ServiceLinkConflict:     "service_link_conflict",
			ServiceLinkInvalid:      "service_link_invalid",
			TargetLost:              "target_lost",
			ActivationFailed:        "activation_failed",
			ServiceUnavailable:      "service_unavailable",
			ServiceNotFound:         "service_not_found",
			InvalidRequest:          "invalid_request",
			ArtifactTooLarge:        "artifact_too_large",
			OperationNotFound:       "not_found"},
		Paths: struct {
			Healthz                 string "yaml:\"healthz\""
			Status                  string "yaml:\"status\""
			Config                  string "yaml:\"config\""
			ConfigValidate          string "yaml:\"configValidate\""
			ConfigReload            string "yaml:\"configReload\""
			Reload                  string "yaml:\"reload\""
			Listeners               string "yaml:\"listeners\""
			Upstreams               string "yaml:\"upstreams\""
			Services                string "yaml:\"services\""
			ServiceSettingsSuffix   string "yaml:\"serviceSettingsSuffix\""
			ServiceRollbackSuffix   string "yaml:\"serviceRollbackSuffix\""
			AdminSurfaces           string "yaml:\"adminSurfaces\""
			AdminPages              string "yaml:\"adminPages\""
			AdminActions            string "yaml:\"adminActions\""
			AdminQueryAction        string "yaml:\"adminQueryAction\""
			Logs                    string "yaml:\"logs\""
			TLS                     string "yaml:\"tls\""
			Renew                   string "yaml:\"renew\""
			Revoke                  string "yaml:\"revoke\""
			Operations              string "yaml:\"operations\""
			OperationResolve        string "yaml:\"operationResolve\""
			Audit                   string "yaml:\"audit\""
			ServiceKeys             string "yaml:\"serviceKeys\""
			ServiceLinks            string "yaml:\"serviceLinks\""
			ServiceLinkTargetSuffix string "yaml:\"serviceLinkTargetSuffix\""
			ServiceIDSeparator      string "yaml:\"serviceIDSeparator\""
			ConfigBundlePlan        string "yaml:\"configBundlePlan\""
			ConfigBundleApply       string "yaml:\"configBundleApply\""
		}{Healthz: "/healthz",
			Status:                  "/api/status",
			Config:                  "",
			ConfigValidate:          "",
			ConfigReload:            "",
			Reload:                  "",
			Listeners:               "",
			Upstreams:               "",
			Services:                "/api/services",
			ServiceSettingsSuffix:   "/settings",
			ServiceRollbackSuffix:   "rollback",
			AdminSurfaces:           "/api/services/admin-surfaces",
			AdminPages:              "admin/pages",
			AdminActions:            "actions",
			AdminQueryAction:        "query",
			Logs:                    "",
			TLS:                     "",
			Renew:                   "",
			Revoke:                  "",
			Operations:              "/api/operations",
			OperationResolve:        "/api/operations/resolve",
			Audit:                   "/api/audit",
			ServiceKeys:             "/api/access/service-keys",
			ServiceLinks:            "/api/service-links",
			ServiceLinkTargetSuffix: "/{callerInstanceId}/{targetInstanceId}",
			ServiceIDSeparator:      "/",
			ConfigBundlePlan:        "/api/config-bundles/plan",
			ConfigBundleApply:       "/api/config-bundles/apply"},
		OperationKinds: struct {
			ServiceSettingsApply    string "yaml:\"serviceSettingsApply\""
			ServiceSettingsRollback string "yaml:\"serviceSettingsRollback\""
			ServiceLinkCreate       string "yaml:\"serviceLinkCreate\""
			ServiceLinkReplace      string "yaml:\"serviceLinkReplace\""
			ServiceLinkDelete       string "yaml:\"serviceLinkDelete\""
		}{ServiceSettingsApply: "service-settings-apply",
			ServiceSettingsRollback: "service-settings-rollback",
			ServiceLinkCreate:       "service-link-create",
			ServiceLinkReplace:      "service-link-replace",
			ServiceLinkDelete:       "service-link-delete"},
		Methods: struct {
			Get    string "yaml:\"get\""
			Post   string "yaml:\"post\""
			Put    string "yaml:\"put\""
			Delete string "yaml:\"delete\""
		}{Get: "GET",
			Post:   "POST",
			Put:    "PUT",
			Delete: "DELETE"},
		JSON: struct {
			RequestID               string "yaml:\"requestId\""
			OperationID             string "yaml:\"operationId\""
			State                   string "yaml:\"state\""
			Items                   string "yaml:\"items\""
			NextCursor              string "yaml:\"nextCursor\""
			YAML                    string "yaml:\"yaml\""
			Digest                  string "yaml:\"digest\""
			Valid                   string "yaml:\"valid\""
			IdempotencyKey          string "yaml:\"idempotencyKey\""
			ErrorCode               string "yaml:\"errorCode\""
			ResourceID              string "yaml:\"resourceId\""
			ExpectedRevision        string "yaml:\"expectedRevision\""
			ID                      string "yaml:\"id\""
			ArtifactDigest          string "yaml:\"artifactDigest\""
			Frontends               string "yaml:\"frontends\""
			Files                   string "yaml:\"files\""
			ReplyTo                 string "yaml:\"replyTo\""
			Status                  string "yaml:\"status\""
			Slug                    string "yaml:\"slug\""
			Route                   string "yaml:\"route\""
			Root                    string "yaml:\"root\""
			CurrentRevision         string "yaml:\"currentRevision\""
			PreviousRevision        string "yaml:\"previousRevision\""
			CreatedAt               string "yaml:\"createdAt\""
			ExpiresAt               string "yaml:\"expiresAt\""
			RevokedAt               string "yaml:\"revokedAt\""
			UpdatedAt               string "yaml:\"updatedAt\""
			ReplicaOutcomes         string "yaml:\"replicaOutcomes\""
			StartedAt               string "yaml:\"startedAt\""
			FinishedAt              string "yaml:\"finishedAt\""
			Result                  string "yaml:\"result\""
			Problem                 string "yaml:\"problem\""
			Timestamp               string "yaml:\"timestamp\""
			Actor                   string "yaml:\"actor\""
			Action                  string "yaml:\"action\""
			Resource                string "yaml:\"resource\""
			DigestBefore            string "yaml:\"digestBefore\""
			DigestAfter             string "yaml:\"digestAfter\""
			Name                    string "yaml:\"name\""
			Role                    string "yaml:\"role\""
			Token                   string "yaml:\"token\""
			Type                    string "yaml:\"type\""
			Address                 string "yaml:\"address\""
			ActiveConnections       string "yaml:\"activeConnections\""
			Healthy                 string "yaml:\"healthy\""
			Capabilities            string "yaml:\"capabilities\""
			Limits                  string "yaml:\"limits\""
			Health                  string "yaml:\"health\""
			Profile                 string "yaml:\"profile\""
			Domain                  string "yaml:\"domain\""
			Serial                  string "yaml:\"serial\""
			NotAfter                string "yaml:\"notAfter\""
			Validity                string "yaml:\"validity\""
			Diagnostics             string "yaml:\"diagnostics\""
			Limit                   string "yaml:\"limit\""
			Cursor                  string "yaml:\"cursor\""
			Kind                    string "yaml:\"kind\""
			Active                  string "yaml:\"active\""
			Drift                   string "yaml:\"drift\""
			RuntimeDigest           string "yaml:\"runtimeDigest\""
			CompositionDigest       string "yaml:\"compositionDigest\""
			DataPlaneReadiness      string "yaml:\"dataPlaneReadiness\""
			Reason                  string "yaml:\"reason\""
			InstanceID              string "yaml:\"instanceId\""
			Capability              string "yaml:\"capability\""
			AllowedNames            string "yaml:\"allowedNames\""
			Revision                string "yaml:\"revision\""
			Config                  string "yaml:\"config\""
			CallerInstanceID        string "yaml:\"callerInstanceId\""
			TargetInstanceID        string "yaml:\"targetInstanceId\""
			Rules                   string "yaml:\"rules\""
			PlacementRule           string "yaml:\"placementRule\""
			Carrier                 string "yaml:\"carrier\""
			Weight                  string "yaml:\"weight\""
			RequiredContracts       string "yaml:\"requiredContracts\""
			ContractID              string "yaml:\"contractId\""
			MinimumVersion          string "yaml:\"minimumVersion\""
			MaximumVersionExclusive string "yaml:\"maximumVersionExclusive\""
		}{RequestID: "requestId",
			OperationID:             "operationId",
			State:                   "state",
			Items:                   "items",
			NextCursor:              "nextCursor",
			YAML:                    "",
			Digest:                  "digest",
			Valid:                   "",
			IdempotencyKey:          "idempotencyKey",
			ErrorCode:               "errorCode",
			ResourceID:              "resourceId",
			ExpectedRevision:        "",
			ID:                      "id",
			ArtifactDigest:          "",
			Frontends:               "",
			Files:                   "",
			ReplyTo:                 "replyTo",
			Status:                  "status",
			Slug:                    "",
			Route:                   "",
			Root:                    "",
			CurrentRevision:         "",
			PreviousRevision:        "",
			CreatedAt:               "createdAt",
			ExpiresAt:               "expiresAt",
			RevokedAt:               "revokedAt",
			UpdatedAt:               "updatedAt",
			ReplicaOutcomes:         "replicaOutcomes",
			StartedAt:               "startedAt",
			FinishedAt:              "finishedAt",
			Result:                  "result",
			Problem:                 "problem",
			Timestamp:               "timestamp",
			Actor:                   "actor",
			Action:                  "action",
			Resource:                "resource",
			DigestBefore:            "digestBefore",
			DigestAfter:             "digestAfter",
			Name:                    "name",
			Role:                    "role",
			Token:                   "token",
			Type:                    "type",
			Address:                 "address",
			ActiveConnections:       "activeConnections",
			Healthy:                 "healthy",
			Capabilities:            "capabilities",
			Limits:                  "limits",
			Health:                  "health",
			Profile:                 "profile",
			Domain:                  "domain",
			Serial:                  "serial",
			NotAfter:                "notAfter",
			Validity:                "validity",
			Diagnostics:             "diagnostics",
			Limit:                   "limit",
			Cursor:                  "cursor",
			Kind:                    "kind",
			Active:                  "active",
			Drift:                   "drift",
			RuntimeDigest:           "runtimeDigest",
			CompositionDigest:       "compositionDigest",
			DataPlaneReadiness:      "dataPlaneReadiness",
			Reason:                  "reason",
			InstanceID:              "instanceId",
			Capability:              "capability",
			AllowedNames:            "allowedNames",
			Revision:                "revision",
			Config:                  "config",
			CallerInstanceID:        "callerInstanceId",
			TargetInstanceID:        "targetInstanceId",
			Rules:                   "rules",
			PlacementRule:           "placementRule",
			Carrier:                 "carrier",
			Weight:                  "weight",
			RequiredContracts:       "requiredContracts",
			ContractID:              "contractId",
			MinimumVersion:          "minimumVersion",
			MaximumVersionExclusive: "maximumVersionExclusive"},
		Headers: struct {
			IfMatch            string "yaml:\"ifMatch\""
			AdminSurfaceDigest string "yaml:\"adminSurfaceDigest\""
			ContentType        string "yaml:\"contentType\""
			ETag               string "yaml:\"etag\""
			RequestID          string "yaml:\"requestId\""
			Location           string "yaml:\"location\""
			RetryAfter         string "yaml:\"retryAfter\""
		}{IfMatch: "If-Match",
			AdminSurfaceDigest: "X-Liapoldus-Admin-Surface-Digest",
			ContentType:        "Content-Type",
			ETag:               "ETag",
			RequestID:          "X-Request-ID",
			Location:           "Location",
			RetryAfter:         "Retry-After"},
		ContentTypes: struct {
			YAML    string "yaml:\"yaml\""
			JSON    string "yaml:\"json\""
			Problem string "yaml:\"problem\""
			Text    string "yaml:\"text\""
		}{YAML: "application/yaml",
			JSON:    "application/json",
			Problem: "application/problem+json",
			Text:    "text/plain"},
		Statuses: struct {
			OK                    string "yaml:\"ok\""
			Ready                 string "yaml:\"ready\""
			Draining              string "yaml:\"draining\""
			Failed                string "yaml:\"failed\""
			Invalid               string "yaml:\"invalid\""
			Publishing            string "yaml:\"publishing\""
			Healthy               string "yaml:\"healthy\""
			Degraded              string "yaml:\"degraded\""
			Unavailable           string "yaml:\"unavailable\""
			Unhealthy             string "yaml:\"unhealthy\""
			Stopped               string "yaml:\"stopped\""
			Renewing              string "yaml:\"renewing\""
			Pending               string "yaml:\"pending\""
			Running               string "yaml:\"running\""
			Completed             string "yaml:\"completed\""
			Succeeded             string "yaml:\"succeeded\""
			Empty                 string "yaml:\"empty\""
			NotReady              string "yaml:\"notReady\""
			SystemReleaseRequired string "yaml:\"systemReleaseRequired\""
			RecoveryRequired      string "yaml:\"recoveryRequired\""
		}{OK: "ok",
			Ready:                 "ready",
			Draining:              "draining",
			Failed:                "failed",
			Invalid:               "invalid",
			Publishing:            "",
			Healthy:               "healthy",
			Degraded:              "degraded",
			Unavailable:           "unavailable",
			Unhealthy:             "unhealthy",
			Stopped:               "stopped",
			Renewing:              "",
			Pending:               "pending",
			Running:               "running",
			Completed:             "completed",
			Succeeded:             "succeeded",
			Empty:                 "empty",
			NotReady:              "not-ready",
			SystemReleaseRequired: "",
			RecoveryRequired:      "recovery-required"},
		Idempotency: struct {
			LimitDefault int    "yaml:\"limitDefault\""
			LimitMax     int    "yaml:\"limitMax\""
			LimitMin     int    "yaml:\"limitMin\""
			KeyMin       int    "yaml:\"keyMin\""
			KeyMax       int    "yaml:\"keyMax\""
			Window       string "yaml:\"window\""
			Key          string "yaml:\"key\""
			ReplyTo      string "yaml:\"replyTo\""
		}{LimitDefault: 0,
			LimitMax: 0,
			LimitMin: 0,
			KeyMin:   1,
			KeyMax:   128,
			Window:   "",
			Key:      "Idempotency-Key",
			ReplyTo:  ""},
		Pagination: struct {
			LimitDefault int "yaml:\"limitDefault\""
			LimitMax     int "yaml:\"limitMax\""
			LimitMin     int "yaml:\"limitMin\""
		}{LimitDefault: 50,
			LimitMax: 100,
			LimitMin: 1},
		OperationState: struct {
			Done     string "yaml:\"done\""
			Accepted string "yaml:\"accepted\""
		}{Done: "",
			Accepted: ""},
		Diagnostics: struct {
			OperationNotFound string "yaml:\"operationNotFound\""
		}{OperationNotFound: "The requested operation does not exist."}}
}

func reconciliationDefinitions() ServiceReconciliationPolicy {
	return ServiceReconciliationPolicy{SchemaVersion: 1,
		ReadinessPollMillis: 5000}
}

func rolloutDefinitions() TrafficRolloutAPIContract {
	return TrafficRolloutAPIContract{CollectionSuffix: "/rollouts",
		MetadataPart:                       "metadata",
		ConfigurationPart:                  "configuration",
		MetadataMediaType:                  "application/json",
		ConfigurationMediaType:             "application/json",
		MultipartMediaType:                 "multipart/form-data",
		MaximumMetadataBytes:               65536,
		EnvelopeOverheadBytes:              65536,
		OperationKind:                      "service-traffic-rollout",
		AuditAction:                        "service_traffic_rollout.create",
		ApprovalStageSegment:               "/stages/",
		ApprovalSuffix:                     "/approve",
		RolloutIDSeparator:                 "/",
		ApprovalOperationKind:              "service-traffic-rollout-stage-approval",
		ApprovalAuditAction:                "service_traffic_rollout.stage_approve",
		ControllerConfirmationAuditAction:  "traffic_rollout.confirm",
		MaximumControllerConfirmationBytes: 16384,
		ControllerJSON: TrafficControllerJSONFields{Rollouts: "rollouts",
			ID:                                  "id",
			ServiceID:                           "serviceId",
			ActiveGeneration:                    "activeGeneration",
			Revision:                            "revision",
			ReleaseSHA256:                       "releaseSha256",
			Stages:                              "stages",
			StageID:                             "stageId",
			State:                               "state",
			LastConfirmedCandidateWeightPercent: "lastConfirmedCandidateWeightPercent",
			CandidateWeightPercent:              "candidateWeightPercent",
			MinimumObservationSeconds:           "minimumObservationSeconds",
			RequireManualApproval:               "requireManualApproval",
			AppliedCandidateWeightPercent:       "appliedCandidateWeightPercent",
			ControllerRevision:                  "controllerRevision"}}
}

func sqliteDefinitions() SQLiteContract {
	return SQLiteContract{Driver: "sqlite",
		ParentDirectoryMode:    0x1c0,
		DatabaseFileMode:       0x180,
		MaxOpenConnections:     1,
		MaxIdleConnections:     1,
		SchemaVersion:          16,
		SystemGroupID:          "system",
		HasMigrationTableQuery: "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')",
		MigrationVersionQuery:  "SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
		SchemaVersionError:     "SQLite schema version is incompatible with this Core build.",
		IntegrityCheckQuery:    "PRAGMA quick_check(1)",
		ForeignKeyCheckQuery:   "PRAGMA foreign_key_check",
		IntegritySuccess:       "ok",
		IntegrityError:         "SQLite state integrity check failed.",
		Pragmas:                "PRAGMA foreign_keys = ON;\nPRAGMA journal_mode = WAL;\nPRAGMA synchronous = FULL;\nPRAGMA busy_timeout = 5000;\n",
		Schema:                 []uint8(nil)}
}
