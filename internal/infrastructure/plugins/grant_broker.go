package plugins

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	pluginsdk "github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

type activeGrant struct {
	secret           []byte
	scope            pluginv1.GrantScope
	instanceID       string
	settingsRevision string
	secretReference  string
	purpose          string
}

type grantBroker struct {
	pluginv1.UnimplementedGrantBrokerServer
	mu               sync.Mutex
	active           map[string]activeGrant
	configSecrets    map[string][]byte
	instanceID       string
	settingsRevision string
	configPurpose    string
}

func newGrantBroker(instanceID, settingsRevision string, configSecrets map[string][]byte, configPurpose string) *grantBroker {
	return &grantBroker{
		active:        make(map[string]activeGrant),
		configSecrets: configSecrets, instanceID: instanceID, settingsRevision: settingsRevision,
		configPurpose: configPurpose,
	}
}

func (b *grantBroker) issueConfig() ([]*pluginv1.ActiveGrant, []string, error) {
	b.mu.Lock()
	revision := b.settingsRevision
	secrets := cloneConfigSecrets(b.configSecrets)
	b.mu.Unlock()
	defer clearConfigSecrets(secrets)
	return b.issueConfigFor(revision, secrets)
}

func (b *grantBroker) issueConfigFor(revision string, secrets map[string][]byte) ([]*pluginv1.ActiveGrant, []string, error) {
	issued := make([]*pluginv1.ActiveGrant, 0, len(secrets))
	handles := make([]string, 0, len(secrets))
	for reference, secret := range secrets {
		if reference == "" || len(secret) == 0 || b.instanceID == "" || revision == "" || b.configPurpose == "" {
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
			instanceID: b.instanceID, settingsRevision: revision,
			secretReference: reference, purpose: b.configPurpose,
		}
		b.mu.Unlock()
		issued = append(issued, &pluginv1.ActiveGrant{
			Handle: handle, Purpose: b.configPurpose,
			Scope:      pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY,
			InstanceId: b.instanceID, SettingsRevision: revision,
			SecretReference: reference,
		})
		handles = append(handles, handle)
	}
	return issued, handles, nil
}

func (b *grantBroker) replaceConfiguration(revision string, secrets map[string][]byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	clearConfigSecrets(b.configSecrets)
	b.configSecrets = cloneConfigSecrets(secrets)
	b.settingsRevision = revision
	for handle, grant := range b.active {
		if grant.settingsRevision == revision {
			continue
		}
		clear(grant.secret)
		delete(b.active, handle)
	}
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
		return nil, pluginsdk.ErrGrantDenied
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	grant, exists := b.active[request.GetHandle()]
	if !exists || request.GetScope() != grant.scope || request.GetPurpose() != grant.purpose {
		return nil, pluginsdk.ErrGrantDenied
	}
	if grant.scope == pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY {
		if request.GetInstanceId() != grant.instanceID || request.GetSettingsRevision() != grant.settingsRevision || request.GetSecretReference() != grant.secretReference || request.GetCapability() != "" || request.GetDomain() != "" {
			return nil, pluginsdk.ErrGrantDenied
		}
		secret := append([]byte(nil), grant.secret...)
		clear(grant.secret)
		delete(b.active, request.GetHandle())
		return &pluginv1.RedeemGrantResponse{Secret: secret}, nil
	}
	return nil, pluginsdk.ErrGrantDenied
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
