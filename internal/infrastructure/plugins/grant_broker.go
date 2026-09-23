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
	capability           string
	purpose              string
	domains              []string
	wildcardDomainPrefix string
}

type grantBroker struct {
	pluginv1.UnimplementedGrantBrokerServer
	mu      sync.Mutex
	active  map[string]activeGrant
	allowed map[string]models.PluginSecretGrant
	secrets map[string]models.Secret
}

func newGrantBroker(grants []models.PluginSecretGrant, secrets map[string]models.Secret) *grantBroker {
	allowed := make(map[string]models.PluginSecretGrant, len(grants))
	for _, grant := range grants {
		allowed[grant.Name] = grant
	}
	return &grantBroker{active: make(map[string]activeGrant), allowed: allowed, secrets: secrets}
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
		b.active[handle] = activeGrant{secret: []byte(secret.Value), capability: capability, purpose: grant.Purpose, domains: append([]string(nil), grant.Domains...), wildcardDomainPrefix: grant.WildcardDomainPrefix}
		b.mu.Unlock()
		issued = append(issued, &pluginv1.ActiveGrant{Handle: handle, Purpose: grant.Purpose, Domains: append([]string(nil), grant.Domains...), Capability: capability})
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
	if !exists || request.GetCapability() != grant.capability || request.GetPurpose() != grant.purpose || !grantDomainAllowed(grant.domains, grant.wildcardDomainPrefix, request.GetDomain()) {
		return nil, transport.ErrGrantDenied
	}
	return &pluginv1.RedeemGrantResponse{Secret: append([]byte(nil), grant.secret...)}, nil
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
