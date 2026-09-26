package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
)

type plugin struct {
	pluginv1.UnimplementedPluginServiceServer
	server  *grpc.Server
	streams atomic.Uint64
}

func (*plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Name:            "fixture",
		ProtocolVersion: pluginprotocol.ProtocolVersion,
		Capabilities:    []string{"forms.submit", "peer.session"},
		CapabilityDescriptors: []*pluginv1.CapabilityDescriptor{
			{Capability: "forms.submit", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_CALL}},
			{Capability: "peer.session", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_TCP, pluginv1.InvocationMode_INVOCATION_MODE_UDP}},
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
	transport := pluginv1.StreamTransport_STREAM_TRANSPORT_UNSPECIFIED
	var streamNumber uint64
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
			if opened || (body.Open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_TCP && body.Open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_UDP) {
				return fmt.Errorf("invalid L4 stream open")
			}
			opened = true
			transport = body.Open.GetTransport()
			streamNumber = p.streams.Add(1)
		case *pluginv1.StreamMessage_Data:
			if !opened || body.Data.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST {
				return fmt.Errorf("invalid L4 stream data")
			}
			payload := append([]byte(nil), body.Data.GetPayload()...)
			if transport == pluginv1.StreamTransport_STREAM_TRANSPORT_UDP {
				payload = append([]byte{byte(streamNumber)}, payload...)
			}
			if err := stream.Send(&pluginv1.StreamMessage{
				Capability: message.GetCapability(),
				Body: &pluginv1.StreamMessage_Data{Data: &pluginv1.StreamData{
					Payload:   payload,
					Direction: pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE,
				}},
			}); err != nil {
				return err
			}
		case *pluginv1.StreamMessage_Close:
			if !opened {
				return fmt.Errorf("L4 stream closed before open")
			}
			if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_Close{Close: body.Close}}); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("unsupported L4 stream frame")
		}
	}
}

func main() {
	listener, err := transport.ListenInherited()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	service := &plugin{}
	service.server = transport.NewServer(service, transport.ServerOptions{})
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdown
		service.server.GracefulStop()
	}()
	if err := service.server.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
