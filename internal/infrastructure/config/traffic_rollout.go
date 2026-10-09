package config

type TrafficRolloutAPIContract struct {
	CollectionSuffix                   string                      `yaml:"pluginCollectionSuffix"`
	MetadataPart                       string                      `yaml:"metadataPart"`
	ConfigurationPart                  string                      `yaml:"configurationPart"`
	MetadataMediaType                  string                      `yaml:"metadataMediaType"`
	ConfigurationMediaType             string                      `yaml:"configurationMediaType"`
	MultipartMediaType                 string                      `yaml:"multipartMediaType"`
	MaximumMetadataBytes               int64                       `yaml:"maximumMetadataBytes"`
	EnvelopeOverheadBytes              int64                       `yaml:"envelopeOverheadBytes"`
	OperationKind                      string                      `yaml:"operationKind"`
	AuditAction                        string                      `yaml:"auditAction"`
	ApprovalStageSegment               string                      `yaml:"approvalStageSegment"`
	ApprovalSuffix                     string                      `yaml:"approvalSuffix"`
	RolloutIDSeparator                 string                      `yaml:"rolloutIDSeparator"`
	ApprovalOperationKind              string                      `yaml:"approvalOperationKind"`
	ApprovalAuditAction                string                      `yaml:"approvalAuditAction"`
	ControllerConfirmationAuditAction  string                      `yaml:"controllerConfirmationAuditAction"`
	MaximumControllerConfirmationBytes int64                       `yaml:"maximumControllerConfirmationBytes"`
	ControllerJSON                     TrafficControllerJSONFields `yaml:"controllerJSON"`
}

type TrafficControllerJSONFields struct {
	Rollouts                            string `yaml:"rollouts"`
	ID                                  string `yaml:"id"`
	PluginID                            string `yaml:"pluginId"`
	ActiveGeneration                    string `yaml:"activeGeneration"`
	Revision                            string `yaml:"revision"`
	ReleaseSHA256                       string `yaml:"releaseSha256"`
	Stages                              string `yaml:"stages"`
	StageID                             string `yaml:"stageId"`
	State                               string `yaml:"state"`
	LastConfirmedCandidateWeightPercent string `yaml:"lastConfirmedCandidateWeightPercent"`
	CandidateWeightPercent              string `yaml:"candidateWeightPercent"`
	MinimumObservationSeconds           string `yaml:"minimumObservationSeconds"`
	RequireManualApproval               string `yaml:"requireManualApproval"`
	AppliedCandidateWeightPercent       string `yaml:"appliedCandidateWeightPercent"`
	ControllerRevision                  string `yaml:"controllerRevision"`
}

func LoadTrafficRolloutAPIContract() (TrafficRolloutAPIContract, error) {
	return rolloutDefinitions(), nil
}
