package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
)

type plugin struct {
	pluginv1.UnimplementedPluginServiceServer
	server *grpc.Server
}

func (*plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Name:            "fixture",
		ProtocolVersion: pluginprotocol.ProtocolVersion,
		Capabilities:    []string{"forms.http", "forms.websocket", "forms.sse"},
		CapabilityDescriptors: []*pluginv1.CapabilityDescriptor{
			{Capability: "forms.http", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM}},
			{Capability: "forms.websocket", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET}},
			{Capability: "forms.sse", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_SSE}},
		},
	}, nil
}

func (*plugin) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}

func (*plugin) ConfigApply(context.Context, *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	return &pluginv1.ConfigApplyResult{Applied: true}, nil
}

func (p *plugin) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go p.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func (p *plugin) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	opened := false
	mode := pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED
	for {
		message, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch body := message.GetBody().(type) {
		case *pluginv1.StreamMessage_Open:
			if opened || !json.Valid(body.Open.GetContextJson()) {
				return fmt.Errorf("invalid stream open")
			}
			mode = body.Open.GetMode()
			opened = true
			switch mode {
			case pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM:
				if err := sendHTTP(stream, message.GetCapability()); err != nil { return err }
			case pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET:
				var open struct { OfferedSubprotocols []string `json:"offeredSubprotocols"` }
				if err := json.Unmarshal(body.Open.GetContextJson(), &open); err != nil { return err }
				protocol := ""
				if len(open.OfferedSubprotocols) > 0 { protocol = open.OfferedSubprotocols[0] }
				if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_WebsocketHandshake{WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: true, Subprotocol: protocol}}}); err != nil { return err }
			case pluginv1.InvocationMode_INVOCATION_MODE_SSE:
				if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Event: "ready", Data: "fixture", Id: "one"}}}); err != nil { return err }
				return nil
		default:
			return fmt.Errorf("unsupported stream mode")
		}
		case *pluginv1.StreamMessage_HttpRequestChunk:
			if !opened || mode != pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM { return fmt.Errorf("unexpected HTTP request chunk") }
			if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_HttpResponseChunk{HttpResponseChunk: &pluginv1.HttpResponseChunk{Payload: append([]byte(nil), body.HttpRequestChunk.GetPayload()...), EndStream: body.HttpRequestChunk.GetEndStream()}}}); err != nil { return err }
		case *pluginv1.StreamMessage_WebsocketMessage:
			if !opened || mode != pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET { return fmt.Errorf("unexpected WebSocket message") }
			if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_WebsocketMessage{WebsocketMessage: &pluginv1.WebSocketMessage{Kind: body.WebsocketMessage.GetKind(), Payload: append([]byte(nil), body.WebsocketMessage.GetPayload()...), Direction: pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE}}}); err != nil { return err }
		case *pluginv1.StreamMessage_Close:
			return nil
		default:
			return fmt.Errorf("unexpected stream frame")
		}
	}
}

func sendHTTP(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage], capability string) error {
	metadata, err := json.Marshal(map[string]any{"headers": map[string]string{"Content-Type": "application/octet-stream"}})
	if err != nil { return err }
	return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseStart{HttpResponseStart: &pluginv1.HttpResponseStart{StatusCode: 200, MetadataJson: metadata}}})
}

func main() {
	listener, err := transport.ListenLoopback()
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	service := &plugin{}
	service.server = transport.NewServer(service, transport.ServerOptions{})
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	go func() { <-shutdown; service.server.GracefulStop() }()
	if err := service.server.Serve(listener); err != nil && !strings.Contains(err.Error(), "stopped") { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
