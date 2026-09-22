package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"google.golang.org/grpc"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

type plugin struct {
	pluginv1.UnimplementedPluginServiceServer
	server *grpc.Server
}

type httpRequest struct {
	Method string `json:"method"`
	Path string `json:"path"`
	Headers map[string]string `json:"headers"`
	Body []byte `json:"body"`
}

func (*plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "forms", ProtocolVersion: "liapoldus.plugin.v1", Capabilities: []string{"forms.submit"}}, nil
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

func (*plugin) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	var input httpRequest
	if err := json.Unmarshal(request.GetPayload(), &input); err != nil {
		return &pluginv1.CallResponse{Code: "invalid_request"}, nil
	}
	_, authPresent := input.Headers["Authorization"]
	_, cookiePresent := input.Headers["Cookie"]
	body, err := json.Marshal(map[string]any{
		"method": input.Method,
		"path": input.Path,
		"body": string(input.Body),
		"authorizationPresent": authPresent,
		"cookiePresent": cookiePresent,
	})
	if err != nil {
		return nil, err
	}
	return &pluginv1.CallResponse{Payload: body}, nil
}

func (p *plugin) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := stream.Send(message); err != nil {
			return err
		}
	}
}

func main() {
	listener, err := transport.ListenLoopback()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	service := &plugin{}
	service.server = transport.NewServer(service, transport.ServerOptions{})
	if err := service.server.Serve(listener); err != nil {
		return
	}
}
