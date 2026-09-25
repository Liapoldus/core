package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
)

type plugin struct {
	pluginv1.UnimplementedPluginServiceServer
	server      *grpc.Server
	marker      string
	startMarker string
}

func (*plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Name: "fixture", ProtocolVersion: pluginprotocol.ProtocolVersion,
		Capabilities: []string{"test.lifecycle"},
		CapabilityDescriptors: []*pluginv1.CapabilityDescriptor{{
			Capability: "test.lifecycle",
			Modes:      []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_CALL},
		}},
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

func (p *plugin) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request.GetCapability() != "test.lifecycle" {
		return &pluginv1.CallResponse{Code: "unsupported_capability"}, nil
	}
	if _, err := os.Stat(p.marker); os.IsNotExist(err) {
		if err := os.WriteFile(p.marker, []byte{}, 0o600); err != nil {
			return nil, err
		}
		os.Exit(23)
	}
	return &pluginv1.CallResponse{Payload: []byte(`{"status":200,"body":"recovered"}`)}, nil
}

func (*plugin) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	for {
		if _, err := stream.Recv(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	service := &plugin{startMarker: os.Args[1], marker: os.Args[2]}
	file, err := os.OpenFile(service.startMarker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		os.Exit(1)
	}
	if err := file.Close(); err != nil {
		os.Exit(1)
	}
	listener, err := transport.ListenLoopback()
	if err != nil {
		os.Exit(1)
	}
	service.server = transport.NewServer(service, transport.ServerOptions{})
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdown
		service.server.GracefulStop()
	}()
	if err := service.server.Serve(listener); err != nil {
		os.Exit(1)
	}
}
