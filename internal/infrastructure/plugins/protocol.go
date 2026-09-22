package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

var (
	ErrProtocolViolation = errors.New("plugin protocol violation")
	ErrPluginUnavailable = errors.New("plugin unavailable")
)

type Client struct {
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

func (c *Client) Close() error { return c.client.Close() }

func (c *Client) Shutdown(ctx context.Context) error {
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	if err := c.client.Shutdown(ctx); err != nil {
		return ErrPluginUnavailable
	}
	return nil
}

func (c *Client) Handshake(ctx context.Context, config []byte) (Handshake, error) {
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
	handshake, err := c.client.Handshake(ctx, config)
	if err != nil {
		return Handshake{}, ErrPluginUnavailable
	}
	if handshake.Manifest == nil {
		return Handshake{}, ErrProtocolViolation
	}
	return Handshake{Manifest: handshake.Manifest}, nil
}

func (c *Client) CallJSON(ctx context.Context, capability string, payload []byte) ([]byte, error) {
	if !json.Valid(payload) {
		return nil, ErrProtocolViolation
	}
	ctx, cancel := c.withDeadline(ctx)
	defer cancel()
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
