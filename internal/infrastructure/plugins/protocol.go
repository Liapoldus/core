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

type ProtocolStream interface {
	Send(*pluginv1.StreamMessage) error
	Recv() (*pluginv1.StreamMessage, error)
	CloseSend() error
}

var (
	ErrProtocolViolation             = errors.New("plugin protocol violation")
	ErrPluginUnavailable             = errors.New("plugin unavailable")
	ErrPluginResourceExhausted error = pluginResourceExhausted{}
	ErrPluginTimeout           error = pluginTimeout{}
)

type pluginTimeout struct{}

func (pluginTimeout) Error() string { return ErrPluginUnavailable.Error() }

type pluginResourceExhausted struct{}

func (pluginResourceExhausted) Error() string { return ErrPluginUnavailable.Error() }

type Client struct {
	mu            sync.RWMutex
	client        *transport.Client
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
	client, err := transport.DialContext(ctx, endpoint)
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
	ctx, cancel := context.WithDeadline(ctx, c.startDeadline)
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
	ctx, cancel := context.WithTimeout(ctx, c.startTimeout)
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
	return c.CallJSONWithGrants(ctx, capability, payload, nil)
}

func (c *Client) CallJSONWithGrants(ctx context.Context, capability string, payload []byte, grants []*pluginv1.ActiveGrant) ([]byte, error) {
	if !json.Valid(payload) {
		return nil, ErrProtocolViolation
	}
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	response, err := c.client.CallWithGrants(ctx, capability, payload, grants)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return nil, context.DeadlineExceeded
		}
		if errors.Is(err, transport.ErrProtocolViolation) {
			return nil, ErrProtocolViolation
		}
		return nil, ErrPluginUnavailable
	}
	return response.GetPayload(), nil
}

func (c *Client) OpenStream(ctx context.Context) (ProtocolStream, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()
	return client.Stream(ctx)
}

func (c *Client) withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.deadline)
}

type Handshake struct{ Manifest *pluginv1.Manifest }
