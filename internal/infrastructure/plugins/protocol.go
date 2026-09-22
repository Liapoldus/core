package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Liapoldus/pluginprotocol/framing"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/proto"
)

var (
	ErrProtocolViolation = errors.New("plugin protocol violation")
	ErrPluginUnavailable = errors.New("plugin unavailable")
)

// Client is the typed unary control/capability boundary for one plugin.
// The stream is deliberately opaque to domain and presentation layers.
type Client struct {
	conn     io.ReadWriteCloser
	mu       sync.Mutex
	next     uint64
	deadline time.Duration
}

func NewClient(conn io.ReadWriteCloser, deadline time.Duration) *Client {
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	return &Client{conn: conn, deadline: deadline, next: 1}
}

func (c *Client) Call(ctx context.Context, method, capability string, payload proto.Message, result proto.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clearDeadline := c.setDeadline(ctx)
	defer clearDeadline()
	request, err := proto.Marshal(payload)
	if err != nil {
		return err
	}
	id := c.next
	c.next++
	frame := &pluginv1.Frame{Kind: pluginv1.FrameKind_CALL, RequestId: id, Payload: mustEnvelope(method, capability, request)}
	if err := framing.Encode(c.conn, frame); err != nil {
		return fmt.Errorf("%w: write: %v", ErrPluginUnavailable, err)
	}
	response, err := framing.Decode(c.conn)
	if err != nil {
		return fmt.Errorf("%w: read: %v", ErrPluginUnavailable, err)
	}
	if response.GetRequestId() != id || response.GetKind() != pluginv1.FrameKind_CALL_RESULT {
		return ErrProtocolViolation
	}
	envelope := new(pluginv1.Envelope)
	if err := proto.Unmarshal(response.GetPayload(), envelope); err != nil {
		return ErrProtocolViolation
	}
	if envelope.GetError() != nil {
		return fmt.Errorf("plugin %s: %s", envelope.GetError().GetCode(), envelope.GetError().GetMessage())
	}
	if result != nil {
		if err := proto.Unmarshal(envelope.GetPayload(), result); err != nil {
			return ErrProtocolViolation
		}
	}
	return nil
}

func (c *Client) CallRaw(ctx context.Context, method, capability string, payload []byte, result proto.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clearDeadline := c.setDeadline(ctx)
	defer clearDeadline()
	id := c.next
	c.next++
	b, err := proto.Marshal(&pluginv1.Envelope{Method: method, Capability: capability, Payload: payload})
	if err != nil {
		return err
	}
	if err := framing.Encode(c.conn, &pluginv1.Frame{Kind: pluginv1.FrameKind_CALL, RequestId: id, Payload: b}); err != nil {
		return fmt.Errorf("%w: write: %v", ErrPluginUnavailable, err)
	}
	response, err := framing.Decode(c.conn)
	if err != nil {
		return fmt.Errorf("%w: read: %v", ErrPluginUnavailable, err)
	}
	if response.GetRequestId() != id || response.GetKind() != pluginv1.FrameKind_CALL_RESULT {
		return ErrProtocolViolation
	}
	envelope := new(pluginv1.Envelope)
	if err := proto.Unmarshal(response.GetPayload(), envelope); err != nil {
		return ErrProtocolViolation
	}
	if envelope.GetError() != nil {
		return fmt.Errorf("plugin %s: %s", envelope.GetError().GetCode(), envelope.GetError().GetMessage())
	}
	if result != nil {
		if err := proto.Unmarshal(envelope.GetPayload(), result); err != nil {
			return ErrProtocolViolation
		}
	}
	return nil
}

func (c *Client) setDeadline(ctx context.Context) func() {
	conn, ok := c.conn.(net.Conn)
	if !ok {
		return func() {}
	}
	deadline := time.Now().Add(c.deadline)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	_ = conn.SetDeadline(deadline)
	return func() { _ = conn.SetDeadline(time.Time{}) }
}

func mustEnvelope(method, capability string, payload []byte) []byte {
	b, _ := proto.Marshal(&pluginv1.Envelope{Method: method, Capability: capability, Payload: payload})
	return b
}

type Handshake struct{ Manifest pluginv1.Manifest }

func (c *Client) Handshake(ctx context.Context, expectedProtocol string, config []byte) (Handshake, error) {
	var manifest pluginv1.Manifest
	if err := c.Call(ctx, "manifest", "", &pluginv1.Manifest{}, &manifest); err != nil {
		return Handshake{}, err
	}
	if manifest.GetProtocolVersion() != expectedProtocol || manifest.GetName() == "" {
		return Handshake{}, ErrProtocolViolation
	}
	var health pluginv1.Health
	if err := c.Call(ctx, "health", "", &pluginv1.Health{}, &health); err != nil || !health.GetReady() {
		return Handshake{}, ErrPluginUnavailable
	}
	var schema pluginv1.ConfigSchema
	if err := c.Call(ctx, "config.schema", "", &pluginv1.ConfigSchema{}, &schema); err != nil {
		return Handshake{}, ErrPluginUnavailable
	}
	var applied pluginv1.ConfigApplyResult
	if err := c.CallRaw(ctx, "config.apply", "", config, &applied); err != nil || !applied.GetApplied() {
		return Handshake{}, ErrPluginUnavailable
	}
	return Handshake{Manifest: manifest}, nil
}
