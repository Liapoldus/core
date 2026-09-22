package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

var (
	ErrProtocolViolation             = errors.New("plugin protocol violation")
	ErrPluginUnavailable             = errors.New("plugin unavailable")
	ErrPluginResourceExhausted error = pluginResourceExhausted{}
)

type pluginResourceExhausted struct{}

func (pluginResourceExhausted) Error() string { return ErrPluginUnavailable.Error() }

type Client struct {
	mu       sync.RWMutex
	client   *transport.Client
	deadline time.Duration
}

func NewClient(endpoint string, deadline time.Duration) (*Client, error) {
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	client, err := transport.DialContext(ctx, endpoint)
	if err != nil {
		return nil, ErrPluginUnavailable
	}
	return &Client{client: client, deadline: deadline}, nil
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

func (c *Client) Handshake(ctx context.Context, config []byte) (Handshake, error) {
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	handshake, err := c.client.Handshake(ctx, config)
	if err != nil {
		return Handshake{}, ErrPluginUnavailable
	}
	if handshake.Manifest == nil {
		return Handshake{}, ErrProtocolViolation
	}
	return Handshake{Manifest: handshake.Manifest}, nil
}

func (c *Client) Reconnect(ctx context.Context, endpoint string, config []byte, expectedName string, capabilities []string) error {
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	replacement, err := transport.DialContext(ctx, endpoint)
	if err != nil {
		return ErrPluginUnavailable
	}
	handshake, err := replacement.Handshake(ctx, config)
	if err != nil {
		_ = replacement.Close()
		return ErrPluginUnavailable
	}
	if handshake.Manifest == nil || handshake.Manifest.GetName() != expectedName || !manifestIncludes(handshake.Manifest.GetCapabilities(), capabilities) {
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

func (c *Client) CallJSON(ctx context.Context, capability string, payload []byte) ([]byte, error) {
	if !json.Valid(payload) {
		return nil, ErrProtocolViolation
	}
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	response, err := c.client.Call(ctx, capability, payload)
	if err != nil {
		if errors.Is(err, transport.ErrProtocolViolation) {
			return nil, ErrProtocolViolation
		}
		return nil, ErrPluginUnavailable
	}
	return response.GetPayload(), nil
}

func (c *Client) withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.deadline)
}

type Handshake struct{ Manifest *pluginv1.Manifest }
