package plugins

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

type activeGrant struct {
	secret               []byte
	scope                pluginv1.GrantScope
	instanceID           string
	settingsRevision     string
	secretReference      string
	capability           string
	purpose              string
	domains              []string
	wildcardDomainPrefix string
}

type grantBroker struct {
	pluginv1.UnimplementedGrantBrokerServer
	mu               sync.Mutex
	active           map[string]activeGrant
	allowed          map[string]models.PluginSecretGrant
	secrets          map[string]models.Secret
	configSecrets    map[string][]byte
	instanceID       string
	settingsRevision string
	configPurpose    string
}

func newGrantBroker(instanceID, settingsRevision string, grants []models.PluginSecretGrant, secrets map[string]models.Secret, configSecrets map[string][]byte, configPurpose string) *grantBroker {
	allowed := make(map[string]models.PluginSecretGrant, len(grants))
	for _, grant := range grants {
		allowed[grant.Name] = grant
	}
	return &grantBroker{
		active: make(map[string]activeGrant), allowed: allowed, secrets: secrets,
		configSecrets: configSecrets, instanceID: instanceID, settingsRevision: settingsRevision,
		configPurpose: configPurpose,
	}
}

func (b *grantBroker) issue(capability string, names []string) ([]*pluginv1.ActiveGrant, []string, error) {
	issued := make([]*pluginv1.ActiveGrant, 0, len(names))
	handles := make([]string, 0, len(names))
	for _, name := range names {
		grant, allowed := b.allowed[name]
		secret, resolved := b.secrets[name]
		if !allowed || !resolved || secret.Value == "" {
			b.revoke(handles)
			return nil, nil, ErrProtocolViolation
		}
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			b.revoke(handles)
			return nil, nil, ErrPluginUnavailable
		}
		handle := base64.RawURLEncoding.EncodeToString(token[:])
		b.mu.Lock()
		b.active[handle] = activeGrant{
			secret: []byte(secret.Value), scope: pluginv1.GrantScope_GRANT_SCOPE_CALL,
			instanceID: b.instanceID, settingsRevision: b.settingsRevision,
			capability: capability, purpose: grant.Purpose,
			domains: append([]string(nil), grant.Domains...), wildcardDomainPrefix: grant.WildcardDomainPrefix,
		}
		b.mu.Unlock()
		issued = append(issued, &pluginv1.ActiveGrant{
			Handle: handle, Purpose: grant.Purpose, Domains: append([]string(nil), grant.Domains...),
			Capability: capability, Scope: pluginv1.GrantScope_GRANT_SCOPE_CALL,
			InstanceId: b.instanceID, SettingsRevision: b.settingsRevision,
		})
		handles = append(handles, handle)
	}
	return issued, handles, nil
}

func (b *grantBroker) issueConfig() ([]*pluginv1.ActiveGrant, []string, error) {
	issued := make([]*pluginv1.ActiveGrant, 0, len(b.configSecrets))
	handles := make([]string, 0, len(b.configSecrets))
	for reference, secret := range b.configSecrets {
		if reference == "" || len(secret) == 0 || b.instanceID == "" || b.settingsRevision == "" || b.configPurpose == "" {
			b.revoke(handles)
			return nil, nil, ErrProtocolViolation
		}
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			b.revoke(handles)
			return nil, nil, ErrPluginUnavailable
		}
		handle := base64.RawURLEncoding.EncodeToString(token[:])
		secretCopy := append([]byte(nil), secret...)
		b.mu.Lock()
		b.active[handle] = activeGrant{
			secret: secretCopy, scope: pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY,
			instanceID: b.instanceID, settingsRevision: b.settingsRevision,
			secretReference: reference, purpose: b.configPurpose,
		}
		b.mu.Unlock()
		issued = append(issued, &pluginv1.ActiveGrant{
			Handle: handle, Purpose: b.configPurpose,
			Scope:      pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY,
			InstanceId: b.instanceID, SettingsRevision: b.settingsRevision,
			SecretReference: reference,
		})
		handles = append(handles, handle)
	}
	return issued, handles, nil
}

func (b *grantBroker) revoke(handles []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, handle := range handles {
		if grant, exists := b.active[handle]; exists {
			for index := range grant.secret {
				grant.secret[index] = 0
			}
			delete(b.active, handle)
		}
	}
}

func (b *grantBroker) RedeemGrant(_ context.Context, request *pluginv1.RedeemGrantRequest) (*pluginv1.RedeemGrantResponse, error) {
	if request == nil {
		return nil, transport.ErrGrantDenied
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	grant, exists := b.active[request.GetHandle()]
	if !exists || request.GetScope() != grant.scope || request.GetPurpose() != grant.purpose {
		return nil, transport.ErrGrantDenied
	}
	if grant.scope == pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY {
		if request.GetInstanceId() != grant.instanceID || request.GetSettingsRevision() != grant.settingsRevision || request.GetSecretReference() != grant.secretReference || request.GetCapability() != "" || request.GetDomain() != "" {
			return nil, transport.ErrGrantDenied
		}
		secret := append([]byte(nil), grant.secret...)
		clear(grant.secret)
		delete(b.active, request.GetHandle())
		return &pluginv1.RedeemGrantResponse{Secret: secret}, nil
	}
	if grant.scope != pluginv1.GrantScope_GRANT_SCOPE_CALL || request.GetCapability() != grant.capability || request.GetInstanceId() != "" && request.GetInstanceId() != grant.instanceID || request.GetSettingsRevision() != "" && request.GetSettingsRevision() != grant.settingsRevision || request.GetSecretReference() != "" || !grantDomainAllowed(grant.domains, grant.wildcardDomainPrefix, request.GetDomain()) {
		return nil, transport.ErrGrantDenied
	}
	return &pluginv1.RedeemGrantResponse{Secret: append([]byte(nil), grant.secret...)}, nil
}

func (b *grantBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for handle, grant := range b.active {
		clear(grant.secret)
		delete(b.active, handle)
	}
	clearConfigSecrets(b.configSecrets)
}

func grantDomainAllowed(allowed []string, wildcardPrefix, requested string) bool {
	if len(allowed) == 0 {
		return requested == ""
	}
	requested = normalizeGrantDomain(requested)
	for _, domain := range allowed {
		domain = normalizeGrantDomain(domain)
		if requested == domain {
			return true
		}
		if wildcardPrefix != "" && strings.HasPrefix(domain, wildcardPrefix) {
			suffix := strings.TrimPrefix(domain, wildcardPrefix)
			if strings.HasSuffix(requested, suffix) && len(requested) > len(suffix) && requested[len(requested)-len(suffix)-1] == '.' {
				return true
			}
		}
	}
	return false
}

func normalizeGrantDomain(domain string) string {
	domain = strings.ToLower(domain)
	if len(domain) > 0 && domain[len(domain)-1] == '.' {
		domain = domain[:len(domain)-1]
	}
	return domain
}
