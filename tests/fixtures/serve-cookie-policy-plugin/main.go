package main

import (
	"context"
	"encoding/json"
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
	server *grpc.Server
}

type request struct {
	Path    string `json:"path"`
	Cookies []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"cookies"`
}

func (*plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Name: "cookie-fixture", ProtocolVersion: pluginprotocol.ProtocolVersion,
		Capabilities: []string{"test.cookie-boundary"},
		CapabilityDescriptors: []*pluginv1.CapabilityDescriptor{{
			Capability: "test.cookie-boundary",
			Modes:      []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_CALL},
		}},
	}, nil
}

func (*plugin) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}

func (*plugin) Bootstrap(_ context.Context, request *pluginv1.BootstrapRequest) (*pluginv1.BootstrapResult, error) {
	return &pluginv1.BootstrapResult{Accepted: request.GetInstanceId() == "cookie-fixture" && request.GetGrantBrokerEndpoint() != ""}, nil
}

func (*plugin) ConfigApply(_ context.Context, apply *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	var settings map[string]json.RawMessage
	if json.Unmarshal(apply.GetConfig(), &settings) != nil {
		return &pluginv1.ConfigApplyResult{}, nil
	}
	return &pluginv1.ConfigApplyResult{Applied: true, SettingsRevision: apply.GetSettingsRevision()}, nil
}

func (instance *plugin) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go instance.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func (*plugin) Call(_ context.Context, call *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	var input request
	if call.GetCapability() != "test.cookie-boundary" || json.Unmarshal(call.GetPayload(), &input) != nil {
		return &pluginv1.CallResponse{Code: "invalid_request"}, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(executable+".called", []byte{}, 0o600); err != nil {
		return nil, err
	}
	cookies, err := json.Marshal(input.Cookies)
	if err != nil {
		return nil, err
	}
	response := struct {
		Status  int    `json:"status"`
		Body    string `json:"body"`
		Cookies []any  `json:"cookies,omitempty"`
	}{Status: 200, Body: string(cookies)}
	if input.Path == "/accepted" {
		response.Cookies = []any{
			map[string]any{"name": "theme", "value": "synthetic-ordinary-value", "path": "/", "secure": true, "httpOnly": false, "sameSite": "Lax"},
			map[string]any{"name": "liap-session", "value": "synthetic-httponly-value", "path": "/", "secure": true, "httpOnly": true, "sameSite": "Lax"},
		}
	}
	if input.Path == "/rejected" {
		response.Cookies = []any{
			map[string]any{"name": "theme", "value": "synthetic-invalid-value", "path": "/", "secure": true, "httpOnly": false, "sameSite": "Lax"},
			map[string]any{"name": "invalid", "value": "synthetic-invalid-value", "path": "/", "sameSite": "None", "secure": false, "httpOnly": true},
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return &pluginv1.CallResponse{Payload: encoded}, nil
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
	listener, err := transport.ListenInherited()
	if err != nil {
		os.Exit(1)
	}
	instance := &plugin{}
	instance.server = transport.NewServer(instance, transport.ServerOptions{})
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdown
		instance.server.GracefulStop()
	}()
	if err := instance.server.Serve(listener); err != nil {
		os.Exit(1)
	}
}
