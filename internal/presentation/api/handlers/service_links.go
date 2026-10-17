package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// serviceLinkRequestMaximumBytes bounds an authored link policy request. Rules
// are small and bounded (identifier and version lengths), so this only guards
// against unbounded bodies.
const (
	serviceLinkRequestMaximumBytes         = 1 << 20
	serviceLinkMaximumRules                = 4
	serviceLinkContractVersionMaximumRunes = 64
)

type serviceLinkRequestError struct{}

func (serviceLinkRequestError) Error() string { return "invalid peer link request" }

type peerContractRangeRequest struct {
	ContractID              string `json:"contractId"`
	MinimumVersion          string `json:"minimumVersion"`
	MaximumVersionExclusive string `json:"maximumVersionExclusive"`
}

type serviceLinkRuleRequest struct {
	PlacementRule     string                     `json:"placementRule"`
	Carrier           string                     `json:"carrier"`
	Weight            uint16                     `json:"weight"`
	RequiredContracts []peerContractRangeRequest `json:"requiredContracts"`
}

type serviceLinkCreateRequest struct {
	CallerInstanceID string                   `json:"callerInstanceId"`
	TargetInstanceID string                   `json:"targetInstanceId"`
	Rules            []serviceLinkRuleRequest `json:"rules"`
}

type serviceLinkReplaceRequest struct {
	Rules []serviceLinkRuleRequest `json:"rules"`
}

// IsServiceLinkCollectionPath reports whether path addresses the service-links
// collection (GET list, POST create).
func IsServiceLinkCollectionPath(deps ServiceDependencies, path string) bool {
	return deps.Management.Paths.ServiceLinks != "" && path == deps.Management.Paths.ServiceLinks
}

// IsServiceLinkDetailPath reports whether path addresses one caller→target pair.
func IsServiceLinkDetailPath(deps ServiceDependencies, path string) bool {
	_, _, matched := serviceLinkPair(deps, path)
	return matched
}

func serviceLinkPair(deps ServiceDependencies, path string) (string, string, bool) {
	base := deps.Management.Paths.ServiceLinks
	separator := deps.Management.Paths.ServiceIDSeparator
	if base == "" || separator == "" || deps.Management.Paths.ServiceLinkTargetSuffix == "" {
		return "", "", false
	}
	resource := strings.TrimPrefix(path, base+separator)
	if resource == path {
		return "", "", false
	}
	parts := strings.Split(resource, separator)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ServiceLinkList returns every durable Core-owned link policy. Absence of a
// policy for a pair means deny.
func ServiceLinkList(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	if deps.ServiceLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	policies, err := deps.ServiceLinks.List(request.Context())
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]any, 0, len(policies))
	for _, policy := range policies {
		items = append(items, serviceLinkPolicyJSON(deps, policy))
	}
	deps.WritePage(response, items, request, requestID)
}

// ServiceLinkCreate stores a new deny-by-default pair policy.
func ServiceLinkCreate(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if deps.ServiceLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	key, ok := serviceLinkIdempotencyKey(deps, request)
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	body, err := serviceLinkBody(deps, request)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	var input serviceLinkCreateRequest
	if err := decodeServiceLinkBody(body, &input); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	policy, err := input.policy()
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceLinkInvalid, requestID)
		return
	}
	audit := serviceLinkAudit(deps, actor, requestID, deps.AuditWords.Audit.Actions.ServiceLinkCreate, policy)
	created, err := deps.ServiceLinks.Create(request.Context(), policy, audit, actor, requestID,
		request.URL.Path, key, serviceLinkDigest(request.Method, request.URL.Path, body))
	if err != nil {
		writeServiceLinkFailure(deps, response, requestID, err, true)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(created.Revision))
	deps.WriteJSON(response, http.StatusCreated, serviceLinkPolicyJSON(deps, created))
}

// ServiceLinkGet returns one pair policy and its CAS revision.
func ServiceLinkGet(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if deps.ServiceLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	callerInstanceID, targetInstanceID, matched := serviceLinkPair(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	policy, err := deps.ServiceLinks.Get(request.Context(), callerInstanceID, targetInstanceID)
	if err != nil {
		writeServiceLinkFailure(deps, response, requestID, err, false)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(policy.Revision))
	deps.WriteJSON(response, http.StatusOK, serviceLinkPolicyJSON(deps, policy))
}

// ServiceLinkReplace atomically replaces the rule set for an existing pair.
func ServiceLinkReplace(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.ServiceLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	callerInstanceID, targetInstanceID, matched := serviceLinkPair(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	expectedRevision, ok := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !ok || expectedRevision < 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	key, ok := serviceLinkIdempotencyKey(deps, request)
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	body, err := serviceLinkBody(deps, request)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	var input serviceLinkReplaceRequest
	if err := decodeServiceLinkBody(body, &input); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	policy, err := input.policy(callerInstanceID, targetInstanceID)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceLinkInvalid, requestID)
		return
	}
	audit := serviceLinkAudit(deps, actor, requestID, deps.AuditWords.Audit.Actions.ServiceLinkReplace, policy)
	replaced, err := deps.ServiceLinks.Replace(request.Context(), policy, expectedRevision, audit, actor, requestID,
		path, key, serviceLinkDigest(request.Method, path, body))
	if err != nil {
		writeServiceLinkFailure(deps, response, requestID, err, false)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(replaced.Revision))
	deps.WriteJSON(response, http.StatusOK, serviceLinkPolicyJSON(deps, replaced))
}

// ServiceLinkDelete removes one pair policy; absence of a policy means deny.
func ServiceLinkDelete(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.ServiceLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	callerInstanceID, targetInstanceID, matched := serviceLinkPair(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	expectedRevision, ok := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !ok || expectedRevision < 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	key, ok := serviceLinkIdempotencyKey(deps, request)
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	policy := models.ServiceLinkPolicy{CallerInstanceID: callerInstanceID, TargetInstanceID: targetInstanceID}
	audit := serviceLinkAudit(deps, actor, requestID, deps.AuditWords.Audit.Actions.ServiceLinkDelete, policy)
	if err := deps.ServiceLinks.Delete(request.Context(), callerInstanceID, targetInstanceID, expectedRevision,
		audit, actor, requestID, request.URL.Path, key, serviceLinkDigest(request.Method, request.URL.Path, nil)); err != nil {
		writeServiceLinkFailure(deps, response, requestID, err, false)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (input serviceLinkCreateRequest) policy() (models.ServiceLinkPolicy, error) {
	policy := models.ServiceLinkPolicy{
		CallerInstanceID: input.CallerInstanceID,
		TargetInstanceID: input.TargetInstanceID,
		Rules:            serviceLinkRules(input.CallerInstanceID, input.TargetInstanceID, input.Rules),
	}
	if err := validateServiceLinkShape(input.Rules); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if err := policy.Validate(); err != nil {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	return policy, nil
}

func (input serviceLinkReplaceRequest) policy(callerInstanceID, targetInstanceID string) (models.ServiceLinkPolicy, error) {
	policy := models.ServiceLinkPolicy{
		CallerInstanceID: callerInstanceID,
		TargetInstanceID: targetInstanceID,
		Rules:            serviceLinkRules(callerInstanceID, targetInstanceID, input.Rules),
	}
	if err := validateServiceLinkShape(input.Rules); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if err := policy.Validate(); err != nil {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	return policy, nil
}

func serviceLinkRules(callerInstanceID, targetInstanceID string, requests []serviceLinkRuleRequest) []models.PeerLinkRule {
	rules := make([]models.PeerLinkRule, 0, len(requests))
	for _, rule := range requests {
		requirements := make([]models.PeerContractRange, 0, len(rule.RequiredContracts))
		for _, requirement := range rule.RequiredContracts {
			requirements = append(requirements, models.PeerContractRange{
				ContractID:              requirement.ContractID,
				MinimumVersion:          requirement.MinimumVersion,
				MaximumVersionExclusive: requirement.MaximumVersionExclusive,
			})
		}
		rules = append(rules, models.PeerLinkRule{
			CallerInstanceID: callerInstanceID, TargetInstanceID: targetInstanceID,
			PlacementRule: rule.PlacementRule, Carrier: rule.Carrier, Weight: rule.Weight,
			RequiredContracts: requirements,
		})
	}
	return rules
}

// validateServiceLinkShape enforces the OpenAPI request bounds that the generic
// model validation does not own: rule count and opaque contract version length.
func validateServiceLinkShape(rules []serviceLinkRuleRequest) error {
	if len(rules) == 0 || len(rules) > serviceLinkMaximumRules {
		return models.PeerLinkPolicyInvalid{}
	}
	for _, rule := range rules {
		for _, requirement := range rule.RequiredContracts {
			if utf8.RuneCountInString(requirement.MinimumVersion) == 0 ||
				utf8.RuneCountInString(requirement.MinimumVersion) > serviceLinkContractVersionMaximumRunes ||
				utf8.RuneCountInString(requirement.MaximumVersionExclusive) == 0 ||
				utf8.RuneCountInString(requirement.MaximumVersionExclusive) > serviceLinkContractVersionMaximumRunes {
				return models.PeerLinkPolicyInvalid{}
			}
		}
	}
	return nil
}

func serviceLinkIdempotencyKey(deps ServiceDependencies, request *http.Request) (string, bool) {
	key := request.Header.Get(deps.Management.Idempotency.Key)
	length := utf8.RuneCountInString(key)
	return key, key != "" && length >= deps.Management.Idempotency.KeyMin && length <= deps.Management.Idempotency.KeyMax
}

func serviceLinkBody(deps ServiceDependencies, request *http.Request) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get(deps.Management.Headers.ContentType))
	if err != nil || mediaType != deps.Management.ContentTypes.JSON {
		return nil, serviceLinkRequestError{}
	}
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, serviceLinkRequestMaximumBytes+1))
	if err != nil || len(raw) > serviceLinkRequestMaximumBytes {
		return nil, serviceLinkRequestError{}
	}
	return raw, nil
}

func decodeServiceLinkBody(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return serviceLinkRequestError{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return serviceLinkRequestError{}
	}
	return nil
}

func serviceLinkDigest(method, path string, body []byte) string {
	hasher := sha256.New()
	hasher.Write([]byte(method))
	hasher.Write([]byte(" "))
	hasher.Write([]byte(path))
	hasher.Write([]byte("\n"))
	hasher.Write(body)
	return hex.EncodeToString(hasher.Sum(nil))
}

func serviceLinkAudit(deps ServiceDependencies, actor, requestID, action string, policy models.ServiceLinkPolicy) models.AuditRecord {
	return models.AuditRecord{
		Actor: actor, Action: action,
		Resource: policy.CallerInstanceID + "->" + policy.TargetInstanceID,
		Result:   deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
	}
}

func serviceLinkPolicyJSON(deps ServiceDependencies, policy models.ServiceLinkPolicy) map[string]any {
	rules := make([]any, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		requirements := make([]any, 0, len(rule.RequiredContracts))
		for _, requirement := range rule.RequiredContracts {
			requirements = append(requirements, map[string]any{
				deps.Management.JSON.ContractID:              requirement.ContractID,
				deps.Management.JSON.MinimumVersion:          requirement.MinimumVersion,
				deps.Management.JSON.MaximumVersionExclusive: requirement.MaximumVersionExclusive,
			})
		}
		rules = append(rules, map[string]any{
			deps.Management.JSON.PlacementRule:     rule.PlacementRule,
			deps.Management.JSON.Carrier:           rule.Carrier,
			deps.Management.JSON.Weight:            rule.Weight,
			deps.Management.JSON.RequiredContracts: requirements,
		})
	}
	return map[string]any{
		deps.Management.JSON.CallerInstanceID: policy.CallerInstanceID,
		deps.Management.JSON.TargetInstanceID: policy.TargetInstanceID,
		deps.Management.JSON.Revision:         policy.Revision,
		deps.Management.JSON.Rules:            rules,
	}
}

func writeServiceLinkFailure(deps ServiceDependencies, response http.ResponseWriter, requestID string, err error, creating bool) {
	var idempotency models.IdempotencyConflict
	var conflict models.PeerLinkPolicyConflict
	var notFound models.PeerLinkPolicyNotFound
	var invalid models.PeerLinkPolicyInvalid
	var ruleInvalid models.PeerLinkRuleInvalid
	var unavailable models.PeerLinkPolicyUnavailable
	switch {
	case errors.As(err, &idempotency):
		deps.WriteCatalogProblem(response, deps.Management.Codes.IdempotencyConflict, requestID)
	case errors.As(err, &notFound):
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.As(err, &conflict):
		if creating {
			deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceLinkConflict, requestID)
			return
		}
		deps.WriteProblem(response, http.StatusPreconditionFailed, deps.Management.Codes.ServiceRevisionConflict, "revision conflict", requestID)
	case errors.As(err, &invalid), errors.As(err, &ruleInvalid):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceLinkInvalid, requestID)
	case errors.As(err, &unavailable):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	default:
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	}
}
