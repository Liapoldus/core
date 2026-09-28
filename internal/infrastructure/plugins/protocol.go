package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	pluginsdk "github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

var (
	ErrProtocolViolation = errors.New("plugin protocol violation")
	ErrPluginUnavailable = errors.New("plugin unavailable")
)

type Client struct {
	mu            sync.RWMutex
	client        *pluginsdk.Client
	deadline      time.Duration
	startTimeout  time.Duration
	startDeadline time.Time
}

func NewClient(endpoint string, deadline, startTimeout time.Duration) (*Client, error) {
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	if startTimeout <= 0 {
		startTimeout = deadline
	}
	startDeadline := time.Now().Add(startTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), startDeadline)
	defer cancel()
	client, err := pluginsdk.DialContext(ctx, endpoint)
	if err != nil {
		return nil, ErrPluginUnavailable
	}
	return &Client{client: client, deadline: deadline, startTimeout: startTimeout, startDeadline: startDeadline}, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.Close()
}

func (c *Client) CheckHealth(ctx context.Context) error {
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if err := c.client.CheckHealth(ctx); err != nil {
		return ErrPluginUnavailable
	}
	return nil
}

// VerifyReady reads the plugin's manifest and checks health without applying
// application settings. Gateway pushes settings through ConfigApply before
// A traffic-serving plugin receives the binding through its own configuration.
func (c *Client) VerifyReady(ctx context.Context) (*pluginv1.Manifest, error) {
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	manifest, err := c.client.Service().Manifest(ctx, &pluginv1.ManifestRequest{})
	if err != nil {
		return nil, ErrPluginUnavailable
	}
	if ValidateManifest(manifest, nil) != nil {
		return nil, ErrProtocolViolation
	}
	if err := c.client.CheckHealth(ctx); err != nil {
		return nil, ErrPluginUnavailable
	}
	return manifest, nil
}

func (c *Client) Shutdown(ctx context.Context) error {
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if err := c.client.Shutdown(ctx); err != nil {
		return ErrPluginUnavailable
	}
	return nil
}

func (c *Client) ApplyConfiguration(ctx context.Context, revision string, configuration []byte, grants []*pluginv1.ActiveGrant) error {
	if revision == "" || !json.Valid(configuration) {
		return ErrProtocolViolation
	}
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	result, err := c.client.Service().ConfigApply(ctx, &pluginv1.ConfigApplyRequest{
		Config: configuration, SettingsRevision: revision, Grants: grants,
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return ErrPluginUnavailable
	}
	if result == nil || !result.GetApplied() || result.GetSettingsRevision() != revision {
		return ErrPluginUnavailable
	}
	return nil
}

func (c *Client) BootstrapAndHandshake(ctx context.Context, instanceID, grantBrokerEndpoint string, config []byte, settingsRevision string, grants []*pluginv1.ActiveGrant) (Handshake, error) {
	ctx, cancel := context.WithDeadline(ctx, c.startDeadline)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	handshake, err := c.client.BootstrapAndHandshake(ctx, &pluginv1.BootstrapRequest{
		InstanceId: instanceID, GrantBrokerEndpoint: grantBrokerEndpoint,
	}, config, settingsRevision, grants)
	if err != nil {
		return Handshake{}, ErrPluginUnavailable
	}
	if ValidateManifest(handshake.Manifest, nil) != nil {
		return Handshake{}, ErrProtocolViolation
	}
	return Handshake{Manifest: handshake.Manifest}, nil
}

func (c *Client) Reconnect(ctx context.Context, endpoint, instanceID, grantBrokerEndpoint string, config []byte, settingsRevision string, grants []*pluginv1.ActiveGrant, expectedName string, capabilities []string) error {
	ctx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()
	replacement, err := pluginsdk.DialContext(ctx, endpoint)
	if err != nil {
		return ErrPluginUnavailable
	}
	handshake, err := replacement.BootstrapAndHandshake(ctx, &pluginv1.BootstrapRequest{
		InstanceId: instanceID, GrantBrokerEndpoint: grantBrokerEndpoint,
	}, config, settingsRevision, grants)
	if err != nil {
		_ = replacement.Close()
		return ErrPluginUnavailable
	}
	if ValidateManifest(handshake.Manifest, capabilities) != nil || handshake.Manifest.GetName() != expectedName {
		_ = replacement.Close()
		return ErrProtocolViolation
	}
	c.mu.Lock()
	previous := c.client
	c.client = replacement
	c.mu.Unlock()
	_ = previous.Close()
	return nil
}

func (c *Client) withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.deadline)
}

type Handshake struct{ Manifest *pluginv1.Manifest }
