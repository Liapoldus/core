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

	"github.com/Liapoldus/core/internal/domain/models"
)

// pluginLinkRequestMaximumBytes bounds an authored link policy request. Rules
// are small and bounded (identifier and version lengths), so this only guards
// against unbounded bodies.
const (
	pluginLinkRequestMaximumBytes         = 1 << 20
	pluginLinkMaximumRules                = 4
	pluginLinkContractVersionMaximumRunes = 64
)

type pluginLinkRequestError struct{}

func (pluginLinkRequestError) Error() string { return "invalid peer link request" }

type peerContractRangeRequest struct {
	ContractID              string `json:"contractId"`
	MinimumVersion          string `json:"minimumVersion"`
	MaximumVersionExclusive string `json:"maximumVersionExclusive"`
}

type pluginLinkRuleRequest struct {
	PlacementRule     string                     `json:"placementRule"`
	Carrier           string                     `json:"carrier"`
	Weight            uint16                     `json:"weight"`
	RequiredContracts []peerContractRangeRequest `json:"requiredContracts"`
}

type pluginLinkCreateRequest struct {
	CallerInstanceID string                  `json:"callerInstanceId"`
	TargetInstanceID string                  `json:"targetInstanceId"`
	Rules            []pluginLinkRuleRequest `json:"rules"`
}

type pluginLinkReplaceRequest struct {
	Rules []pluginLinkRuleRequest `json:"rules"`
}

// IsPluginLinkCollectionPath reports whether path addresses the plugin-links
// collection (GET list, POST create).
func IsPluginLinkCollectionPath(deps PluginDependencies, path string) bool {
	return deps.Management.Paths.PluginLinks != "" && path == deps.Management.Paths.PluginLinks
}

// IsPluginLinkDetailPath reports whether path addresses one caller→target pair.
func IsPluginLinkDetailPath(deps PluginDependencies, path string) bool {
	_, _, matched := pluginLinkPair(deps, path)
	return matched
}

func pluginLinkPair(deps PluginDependencies, path string) (string, string, bool) {
	base := deps.Management.Paths.PluginLinks
	separator := deps.Management.Paths.PluginIDSeparator
	if base == "" || separator == "" || deps.Management.Paths.PluginLinkTargetSuffix == "" {
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

// PluginLinkList returns every durable Core-owned link policy. Absence of a
// policy for a pair means deny.
func PluginLinkList(deps PluginDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	if deps.PluginLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	policies, err := deps.PluginLinks.List(request.Context())
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]any, 0, len(policies))
	for _, policy := range policies {
		items = append(items, pluginLinkPolicyJSON(deps, policy))
	}
	deps.WritePage(response, items, request, requestID)
}

// PluginLinkCreate stores a new deny-by-default pair policy.
func PluginLinkCreate(deps PluginDependencies, response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if deps.PluginLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	key, ok := pluginLinkIdempotencyKey(deps, request)
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	body, err := pluginLinkBody(deps, request)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	var input pluginLinkCreateRequest
	if err := decodePluginLinkBody(body, &input); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	policy, err := input.policy()
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginLinkInvalid, requestID)
		return
	}
	audit := pluginLinkAudit(deps, actor, requestID, deps.AuditWords.Audit.Actions.PluginLinkCreate, policy)
	created, err := deps.PluginLinks.Create(request.Context(), policy, audit, actor, requestID,
		request.URL.Path, key, pluginLinkDigest(request.Method, request.URL.Path, body))
	if err != nil {
		writePluginLinkFailure(deps, response, requestID, err, true)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(created.Revision))
	deps.WriteJSON(response, http.StatusCreated, pluginLinkPolicyJSON(deps, created))
}

// PluginLinkGet returns one pair policy and its CAS revision.
func PluginLinkGet(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if deps.PluginLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	callerInstanceID, targetInstanceID, matched := pluginLinkPair(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	policy, err := deps.PluginLinks.Get(request.Context(), callerInstanceID, targetInstanceID)
	if err != nil {
		writePluginLinkFailure(deps, response, requestID, err, false)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(policy.Revision))
	deps.WriteJSON(response, http.StatusOK, pluginLinkPolicyJSON(deps, policy))
}

// PluginLinkReplace atomically replaces the rule set for an existing pair.
func PluginLinkReplace(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.PluginLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	callerInstanceID, targetInstanceID, matched := pluginLinkPair(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	expectedRevision, ok := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !ok || expectedRevision < 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	key, ok := pluginLinkIdempotencyKey(deps, request)
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	body, err := pluginLinkBody(deps, request)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	var input pluginLinkReplaceRequest
	if err := decodePluginLinkBody(body, &input); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	policy, err := input.policy(callerInstanceID, targetInstanceID)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginLinkInvalid, requestID)
		return
	}
	audit := pluginLinkAudit(deps, actor, requestID, deps.AuditWords.Audit.Actions.PluginLinkReplace, policy)
	replaced, err := deps.PluginLinks.Replace(request.Context(), policy, expectedRevision, audit, actor, requestID,
		path, key, pluginLinkDigest(request.Method, path, body))
	if err != nil {
		writePluginLinkFailure(deps, response, requestID, err, false)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(replaced.Revision))
	deps.WriteJSON(response, http.StatusOK, pluginLinkPolicyJSON(deps, replaced))
}

// PluginLinkDelete removes one pair policy; absence of a policy means deny.
func PluginLinkDelete(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.PluginLinks == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	callerInstanceID, targetInstanceID, matched := pluginLinkPair(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	expectedRevision, ok := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !ok || expectedRevision < 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	key, ok := pluginLinkIdempotencyKey(deps, request)
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	policy := models.PluginLinkPolicy{CallerInstanceID: callerInstanceID, TargetInstanceID: targetInstanceID}
	audit := pluginLinkAudit(deps, actor, requestID, deps.AuditWords.Audit.Actions.PluginLinkDelete, policy)
	if err := deps.PluginLinks.Delete(request.Context(), callerInstanceID, targetInstanceID, expectedRevision,
		audit, actor, requestID, request.URL.Path, key, pluginLinkDigest(request.Method, request.URL.Path, nil)); err != nil {
		writePluginLinkFailure(deps, response, requestID, err, false)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (input pluginLinkCreateRequest) policy() (models.PluginLinkPolicy, error) {
	policy := models.PluginLinkPolicy{
		CallerInstanceID: input.CallerInstanceID,
		TargetInstanceID: input.TargetInstanceID,
		Rules:            pluginLinkRules(input.CallerInstanceID, input.TargetInstanceID, input.Rules),
	}
	if err := validatePluginLinkShape(input.Rules); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if err := policy.Validate(); err != nil {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	return policy, nil
}

func (input pluginLinkReplaceRequest) policy(callerInstanceID, targetInstanceID string) (models.PluginLinkPolicy, error) {
	policy := models.PluginLinkPolicy{
		CallerInstanceID: callerInstanceID,
		TargetInstanceID: targetInstanceID,
		Rules:            pluginLinkRules(callerInstanceID, targetInstanceID, input.Rules),
	}
	if err := validatePluginLinkShape(input.Rules); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if err := policy.Validate(); err != nil {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	return policy, nil
}

func pluginLinkRules(callerInstanceID, targetInstanceID string, requests []pluginLinkRuleRequest) []models.PeerLinkRule {
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

// validatePluginLinkShape enforces the OpenAPI request bounds that the generic
// model validation does not own: rule count and opaque contract version length.
func validatePluginLinkShape(rules []pluginLinkRuleRequest) error {
	if len(rules) == 0 || len(rules) > pluginLinkMaximumRules {
		return models.PeerLinkPolicyInvalid{}
	}
	for _, rule := range rules {
		for _, requirement := range rule.RequiredContracts {
			if utf8.RuneCountInString(requirement.MinimumVersion) == 0 ||
				utf8.RuneCountInString(requirement.MinimumVersion) > pluginLinkContractVersionMaximumRunes ||
				utf8.RuneCountInString(requirement.MaximumVersionExclusive) == 0 ||
				utf8.RuneCountInString(requirement.MaximumVersionExclusive) > pluginLinkContractVersionMaximumRunes {
				return models.PeerLinkPolicyInvalid{}
			}
		}
	}
	return nil
}

func pluginLinkIdempotencyKey(deps PluginDependencies, request *http.Request) (string, bool) {
	key := request.Header.Get(deps.Management.Idempotency.Key)
	length := utf8.RuneCountInString(key)
	return key, key != "" && length >= deps.Management.Idempotency.KeyMin && length <= deps.Management.Idempotency.KeyMax
}

func pluginLinkBody(deps PluginDependencies, request *http.Request) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get(deps.Management.Headers.ContentType))
	if err != nil || mediaType != deps.Management.ContentTypes.JSON {
		return nil, pluginLinkRequestError{}
	}
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, pluginLinkRequestMaximumBytes+1))
	if err != nil || len(raw) > pluginLinkRequestMaximumBytes {
		return nil, pluginLinkRequestError{}
	}
	return raw, nil
}

func decodePluginLinkBody(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return pluginLinkRequestError{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return pluginLinkRequestError{}
	}
	return nil
}

func pluginLinkDigest(method, path string, body []byte) string {
	hasher := sha256.New()
	hasher.Write([]byte(method))
	hasher.Write([]byte(" "))
	hasher.Write([]byte(path))
	hasher.Write([]byte("\n"))
	hasher.Write(body)
	return hex.EncodeToString(hasher.Sum(nil))
}

func pluginLinkAudit(deps PluginDependencies, actor, requestID, action string, policy models.PluginLinkPolicy) models.AuditRecord {
	return models.AuditRecord{
		Actor: actor, Action: action,
		Resource: policy.CallerInstanceID + "->" + policy.TargetInstanceID,
		Result:   deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
	}
}

func pluginLinkPolicyJSON(deps PluginDependencies, policy models.PluginLinkPolicy) map[string]any {
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

func writePluginLinkFailure(deps PluginDependencies, response http.ResponseWriter, requestID string, err error, creating bool) {
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
			deps.WriteCatalogProblem(response, deps.Management.Codes.PluginLinkConflict, requestID)
			return
		}
		deps.WriteProblem(response, http.StatusPreconditionFailed, deps.Management.Codes.PluginRevisionConflict, "revision conflict", requestID)
	case errors.As(err, &invalid), errors.As(err, &ruleInvalid):
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginLinkInvalid, requestID)
	case errors.As(err, &unavailable):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	default:
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	}
}
