package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	protocoltransport "github.com/Liapoldus/pluginprotocol/transport"
)

type L4StreamContext struct {
	Transport   string
	Connection  string
	Source      string
	Destination string
	SNI         string
	ALPN        string
}

type L4Session interface {
	Exchange([]byte) (L4Response, error)
	Close() error
}

type L4Stream struct {
	stream     ProtocolStream
	capability string
	ctx        context.Context
	cancel     context.CancelFunc
	release    func()
	checkRSS   func() bool
	finishOnce sync.Once
}

type l4OpenContext struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SNI         string `json:"sni,omitempty"`
	ALPN        string `json:"alpn,omitempty"`
}

func (c *CapabilityClient) OpenL4Stream(ctx context.Context, capability string, request L4StreamContext) (L4Session, error) {
	if err := c.validateCapability(capability); err != nil {
		return nil, err
	}
	transportKind, err := streamTransport(request.Transport)
	if err != nil || strings.TrimSpace(request.Connection) == "" || strings.TrimSpace(request.Source) == "" || strings.TrimSpace(request.Destination) == "" {
		return nil, ErrProtocolViolation
	}
	select {
	case c.active <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	streamContext, cancel := context.WithCancel(ctx)
	stream, err := c.client.OpenStream(streamContext)
	if err != nil {
		cancel()
		<-c.active
		return nil, c.classifyStreamError(ctx, err)
	}
	openContext, err := json.Marshal(l4OpenContext{
		Kind:        request.Transport,
		Source:      request.Source,
		Destination: request.Destination,
		SNI:         request.SNI,
		ALPN:        request.ALPN,
	})
	if err != nil {
		cancel()
		<-c.active
		return nil, ErrProtocolViolation
	}
	connection := &L4Stream{
		stream:     stream,
		capability: capability,
		ctx:        streamContext,
		cancel:     cancel,
		release:    func() { <-c.active },
		checkRSS:   c.enforceRSSLimit,
	}
	if err := stream.Send(&pluginv1.StreamMessage{
		Capability: capability,
		Body: &pluginv1.StreamMessage_Open{Open: &pluginv1.StreamOpen{
			Transport:    transportKind,
			ConnectionId: request.Connection,
			ContextJson:  openContext,
		}},
	}); err != nil {
		connection.finish()
		return nil, c.classifyStreamError(ctx, err)
	}
	return connection, nil
}

func streamTransport(value string) (pluginv1.StreamTransport, error) {
	switch value {
	case "tcp":
		return pluginv1.StreamTransport_STREAM_TRANSPORT_TCP, nil
	case "udp":
		return pluginv1.StreamTransport_STREAM_TRANSPORT_UDP, nil
	default:
		return pluginv1.StreamTransport_STREAM_TRANSPORT_UNSPECIFIED, ErrProtocolViolation
	}
}

func (s *L4Stream) Exchange(payload []byte) (L4Response, error) {
	if len(payload) > protocoltransport.MaxStreamMessageBytes {
		s.finish()
		return L4Response{}, ErrPluginResourceExhausted
	}
	if err := s.stream.Send(&pluginv1.StreamMessage{
		Capability: s.capability,
		Body: &pluginv1.StreamMessage_Data{Data: &pluginv1.StreamData{
			Payload:   append([]byte(nil), payload...),
			Direction: pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST,
		}},
	}); err != nil {
		s.finish()
		return L4Response{}, classifyL4StreamError(s.ctx, err)
	}
	message, err := s.stream.Recv()
	if err != nil {
		s.finish()
		return L4Response{}, classifyL4StreamError(s.ctx, err)
	}
	if data := message.GetData(); data != nil && data.GetDirection() == pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE {
		if s.checkRSS() {
			s.finish()
			return L4Response{}, ErrPluginResourceExhausted
		}
		return L4Response{Payload: append([]byte(nil), data.GetPayload()...)}, nil
	}
	if closeMessage := message.GetClose(); closeMessage != nil {
		s.finish()
		switch closeMessage.GetCode() {
		case pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL, pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_DROP:
			return L4Response{Drop: true}, nil
		case pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_ERROR:
			return L4Response{}, ErrPluginUnavailable
		default:
			return L4Response{}, ErrProtocolViolation
		}
	}
	s.finish()
	return L4Response{}, ErrProtocolViolation
}

func (s *L4Stream) Close() error {
	defer s.finish()
	if err := s.stream.Send(&pluginv1.StreamMessage{
		Capability: s.capability,
		Body:       &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL}},
	}); err != nil {
		return classifyL4StreamError(s.ctx, err)
	}
	if err := s.stream.CloseSend(); err != nil {
		return classifyL4StreamError(s.ctx, err)
	}
	message, err := s.stream.Recv()
	if err != nil {
		return classifyL4StreamError(s.ctx, err)
	}
	closeMessage := message.GetClose()
	if closeMessage == nil {
		return ErrProtocolViolation
	}
	switch closeMessage.GetCode() {
	case pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL, pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_DROP:
		return nil
	case pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_ERROR:
		return ErrPluginUnavailable
	default:
		return ErrProtocolViolation
	}
}

func (s *L4Stream) finish() {
	s.finishOnce.Do(func() {
		s.cancel()
		s.release()
	})
}

func (c *CapabilityClient) classifyStreamError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrPluginTimeout
	}
	if c.enforceRSSLimit() {
		return ErrPluginResourceExhausted
	}
	return ErrPluginUnavailable
}

func classifyL4StreamError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrPluginTimeout
	}
	return ErrPluginUnavailable
}
