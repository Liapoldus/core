package application

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

var ErrInvalidTrafficRolloutPlan = trafficRolloutPlanInvalid{}

type trafficRolloutPlanInvalid struct{}

func (trafficRolloutPlanInvalid) Error() string { return "" }

// ParseTrafficRolloutPlan validates the transport-neutral stage plan before any
// generation, operation, or audit record is written.
func ParseTrafficRolloutPlan(raw []byte) (models.TrafficRolloutPlan, error) {
	uniqueDecoder := json.NewDecoder(bytes.NewReader(raw))
	document, err := decodeUniqueJSON(uniqueDecoder)
	if err != nil || !validTrafficRolloutFields(document) {
		return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
	}
	if err := uniqueDecoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
	}

	var plan models.TrafficRolloutPlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
	}
	for index := range plan.Stages {
		plan.Stages[index].RequireManualApproval = true
	}

	digest, err := hex.DecodeString(plan.ReleaseSHA256)
	if err != nil || len(digest) != 32 || strings.ToLower(plan.ReleaseSHA256) != plan.ReleaseSHA256 {
		return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
	}
	if len(plan.Targets) == 0 || len(plan.Stages) == 0 {
		return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
	}
	seenReplicas := make(map[string]struct{}, len(plan.Targets))
	for _, target := range plan.Targets {
		if target.ReplicaID == "" || target.Incarnation == "" || strings.TrimSpace(target.ReplicaID) != target.ReplicaID || strings.TrimSpace(target.Incarnation) != target.Incarnation {
			return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
		}
		if _, exists := seenReplicas[target.ReplicaID]; exists {
			return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
		}
		seenReplicas[target.ReplicaID] = struct{}{}
	}
	seenStages := make(map[string]struct{}, len(plan.Stages))
	previousWeight := 0
	for _, stage := range plan.Stages {
		if !validTrafficRolloutStageID(stage.ID) || stage.MinimumObservationSeconds < 0 ||
			stage.CandidateWeightPercent <= previousWeight || stage.CandidateWeightPercent > 100 {
			return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
		}
		if _, exists := seenStages[stage.ID]; exists {
			return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
		}
		seenStages[stage.ID] = struct{}{}
		previousWeight = stage.CandidateWeightPercent
	}
	if previousWeight != 100 {
		return models.TrafficRolloutPlan{}, ErrInvalidTrafficRolloutPlan
	}
	return plan, nil
}

func validTrafficRolloutStageID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		valid := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if index > 0 {
			valid = valid || character == '.' || character == '_' || character == '-'
		}
		if !valid {
			return false
		}
	}
	return true
}

func validTrafficRolloutFields(document any) bool {
	root, ok := document.(map[string]any)
	if !ok || !hasOnlyFields(root, []string{"releaseSha256", "targets", "stages"}) ||
		!hasRequiredFields(root, []string{"releaseSha256", "targets", "stages"}) {
		return false
	}
	targets, ok := root["targets"].([]any)
	if !ok {
		return false
	}
	for _, item := range targets {
		target, ok := item.(map[string]any)
		if !ok || !hasOnlyFields(target, []string{"replicaId", "incarnation"}) ||
			!hasRequiredFields(target, []string{"replicaId", "incarnation"}) {
			return false
		}
	}
	stages, ok := root["stages"].([]any)
	if !ok {
		return false
	}
	for _, item := range stages {
		stage, ok := item.(map[string]any)
		if !ok || !hasOnlyFields(stage, []string{"id", "candidateWeightPercent", "minimumObservationSeconds", "requireManualApproval"}) ||
			!hasRequiredFields(stage, []string{"id", "candidateWeightPercent", "minimumObservationSeconds"}) {
			return false
		}
		if required, exists := stage["requireManualApproval"]; exists && required != true {
			return false
		}
	}
	return true
}

func hasOnlyFields(object map[string]any, allowed []string) bool {
	for field := range object {
		found := false
		for _, candidate := range allowed {
			if field == candidate {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func hasRequiredFields(object map[string]any, required []string) bool {
	for _, field := range required {
		if _, exists := object[field]; !exists {
			return false
		}
	}
	return true
}

func decodeUniqueJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, ErrInvalidTrafficRolloutPlan
			}
			if _, exists := object[key]; exists {
				return nil, ErrInvalidTrafficRolloutPlan
			}
			value, err := decodeUniqueJSON(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		_, err = decoder.Token()
		return object, err
	case '[':
		values := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueJSON(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = decoder.Token()
		return values, err
	default:
		return nil, ErrInvalidTrafficRolloutPlan
	}
}
