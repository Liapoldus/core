package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type PluginSecretGrantService struct {
	Configurations      interfaces.PluginRetainedConfigurationReader
	ResolveSecret       interfaces.PluginSecretReferenceResolver
	GrantTTL            time.Duration
	MaximumValueBytes   int64
	MaximumOutstanding  int
	MaximumReferenceLen int
	MaximumPurposeLen   int
	Now                 func() time.Time
	Random              io.Reader

	mu     sync.Mutex
	grants map[string]pluginSecretGrantEntry
}

type pluginSecretGrantEntry struct {
	models.PluginSecretGrantReceipt
	InstanceID  string
	Fingerprint string
	Spent       bool
}

func (service *PluginSecretGrantService) Issue(
	ctx context.Context,
	identity models.PluginReplicaIdentity,
	reference, purpose, generation string,
) (models.PluginSecretGrantReceipt, error) {
	if service == nil || service.Configurations == nil || service.ResolveSecret == nil ||
		service.GrantTTL <= 0 || service.MaximumValueBytes <= 0 || service.MaximumOutstanding <= 0 ||
		service.Now == nil || service.Random == nil || identity.InstanceID == "" || identity.Fingerprint == "" ||
		reference == "" || len(reference) > service.MaximumReferenceLen || purpose == "" || len(purpose) > service.MaximumPurposeLen {
		return models.PluginSecretGrantReceipt{}, grantFailure(models.PluginSecretGrantUnavailable)
	}
	generationNumber, err := strconv.ParseInt(generation, 10, 64)
	if err != nil || generationNumber < 1 || strconv.FormatInt(generationNumber, 10) != generation {
		return models.PluginSecretGrantReceipt{}, grantFailure(models.PluginSecretGrantDenied)
	}
	active, isActive, err := service.Configurations.Retained(ctx, identity.InstanceID, generationNumber)
	if err != nil || !isActive || active.Revision != generationNumber || !documentContainsString(active.SettingsJSON, reference) {
		return models.PluginSecretGrantReceipt{}, grantFailure(models.PluginSecretGrantDenied)
	}
	if err := ctx.Err(); err != nil {
		return models.PluginSecretGrantReceipt{}, grantFailure(models.PluginSecretGrantUnavailable)
	}

	now := service.Now()
	var entropy [32]byte
	if _, err := io.ReadFull(service.Random, entropy[:]); err != nil {
		return models.PluginSecretGrantReceipt{}, grantFailure(models.PluginSecretGrantUnavailable)
	}
	handle := base64.RawURLEncoding.EncodeToString(entropy[:])
	clear(entropy[:])
	receipt := models.PluginSecretGrantReceipt{
		Handle: handle, Reference: reference, Purpose: purpose, Generation: generation,
		ExpiresAt: now.Add(service.GrantTTL).UTC(),
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.expireLocked(now)
	if len(service.grants) >= service.MaximumOutstanding {
		return models.PluginSecretGrantReceipt{}, grantFailure(models.PluginSecretGrantUnavailable)
	}
	if service.grants == nil {
		service.grants = make(map[string]pluginSecretGrantEntry)
	}
	if _, exists := service.grants[handle]; exists {
		return models.PluginSecretGrantReceipt{}, grantFailure(models.PluginSecretGrantUnavailable)
	}
	service.grants[handle] = pluginSecretGrantEntry{
		PluginSecretGrantReceipt: receipt, InstanceID: identity.InstanceID, Fingerprint: identity.Fingerprint,
	}
	return receipt, nil
}

func (service *PluginSecretGrantService) Redeem(
	ctx context.Context,
	identity models.PluginReplicaIdentity,
	handle string,
) ([]byte, error) {
	if service == nil || service.Configurations == nil || service.ResolveSecret == nil ||
		service.MaximumValueBytes <= 0 || service.Now == nil || identity.InstanceID == "" || identity.Fingerprint == "" || handle == "" {
		return nil, grantFailure(models.PluginSecretGrantUnavailable)
	}
	service.mu.Lock()
	entry, found := service.grants[handle]
	if !found {
		service.mu.Unlock()
		return nil, grantFailure(models.PluginSecretGrantUnknown)
	}
	if entry.InstanceID != identity.InstanceID || entry.Fingerprint != identity.Fingerprint {
		service.mu.Unlock()
		return nil, grantFailure(models.PluginSecretGrantDenied)
	}
	if entry.Spent {
		service.mu.Unlock()
		return nil, grantFailure(models.PluginSecretGrantSpent)
	}
	entry.Spent = true
	service.grants[handle] = entry
	now := service.Now()
	service.mu.Unlock()
	if !entry.ExpiresAt.After(now) {
		return nil, grantFailure(models.PluginSecretGrantExpired)
	}
	if err := ctx.Err(); err != nil {
		return nil, grantFailure(models.PluginSecretGrantUnavailable)
	}
	generation, err := strconv.ParseInt(entry.Generation, 10, 64)
	if err != nil {
		return nil, grantFailure(models.PluginSecretGrantDenied)
	}
	active, isActive, err := service.Configurations.Retained(ctx, entry.InstanceID, generation)
	if err != nil || !isActive || active.Revision != generation || !documentContainsString(active.SettingsJSON, entry.Reference) {
		return nil, grantFailure(models.PluginSecretGrantDenied)
	}
	value, err := service.ResolveSecret(ctx, entry.Reference, service.MaximumValueBytes)
	if err != nil || int64(len(value)) > service.MaximumValueBytes {
		clear(value)
		return nil, grantFailure(models.PluginSecretGrantUnavailable)
	}
	return value, nil
}

func (service *PluginSecretGrantService) expireLocked(now time.Time) {
	for handle, grant := range service.grants {
		if !grant.ExpiresAt.After(now) {
			delete(service.grants, handle)
		}
	}
}

func grantFailure(kind models.PluginSecretGrantFailureKind) error {
	return models.PluginSecretGrantFailure{Kind: kind}
}

// documentContainsString treats plugin configuration as opaque JSON: it only
// verifies that a requested reference occurs as one complete JSON string value.
func documentContainsString(raw []byte, target string) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	return containsJSONString(value, target)
}

func containsJSONString(value any, target string) bool {
	switch typed := value.(type) {
	case string:
		return typed == target
	case []any:
		for _, item := range typed {
			if containsJSONString(item, target) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsJSONString(item, target) {
				return true
			}
		}
	}
	return false
}
