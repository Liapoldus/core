package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
)

type plugin struct {
	pluginv1.UnimplementedPluginServiceServer
	server        *grpc.Server
	marker        string
	startMarker   string
	grantEndpoint string
	instanceID    string
	configured    bool
	secret        []byte
	mu            sync.RWMutex
}

func (p *plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	p.mu.RLock()
	name := p.instanceID
	p.mu.RUnlock()
	return &pluginv1.Manifest{
		Name: name, ProtocolVersion: pluginprotocol.ProtocolVersion,
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

func (p *plugin) Bootstrap(_ context.Context, request *pluginv1.BootstrapRequest) (*pluginv1.BootstrapResult, error) {
	accepted := request.GetInstanceId() != "" && request.GetGrantBrokerEndpoint() != ""
	if accepted {
		p.mu.Lock()
		p.instanceID = request.GetInstanceId()
		p.grantEndpoint = request.GetGrantBrokerEndpoint()
		p.mu.Unlock()
	}
	return &pluginv1.BootstrapResult{Accepted: accepted}, nil
}

func (p *plugin) ConfigApply(_ context.Context, request *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	var settings struct {
		Credential string `json:"credential"`
	}
	p.mu.RLock()
	grantEndpoint := p.grantEndpoint
	instanceID := p.instanceID
	p.mu.RUnlock()
	if json.Unmarshal(request.GetConfig(), &settings) != nil || grantEndpoint == "" {
		return &pluginv1.ConfigApplyResult{}, nil
	}
	if settings.Credential == "" && len(request.GetGrants()) == 0 {
		p.mu.Lock()
		p.configured = true
		p.mu.Unlock()
		return &pluginv1.ConfigApplyResult{Applied: true, SettingsRevision: request.GetSettingsRevision()}, nil
	}
	if len(request.GetGrants()) != 1 {
		return &pluginv1.ConfigApplyResult{}, nil
	}
	grant := request.GetGrants()[0]
	if settings.Credential != grant.GetSecretReference() || grant.GetInstanceId() != instanceID || grant.GetSettingsRevision() != request.GetSettingsRevision() || grant.GetScope() != pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY {
		return &pluginv1.ConfigApplyResult{}, nil
	}
	bootstrap := &pluginv1.BootstrapRequest{InstanceId: instanceID, GrantBrokerEndpoint: grantEndpoint}
	client, err := transport.DialGrantBrokerFromBootstrapContext(context.Background(), bootstrap, nil)
	if err != nil {
		return &pluginv1.ConfigApplyResult{}, nil
	}
	defer client.Close()
	secret, err := client.RedeemConfig(context.Background(), grant)
	if err != nil {
		return &pluginv1.ConfigApplyResult{}, nil
	}
	p.mu.Lock()
	clear(p.secret)
	p.secret = append(p.secret[:0], secret...)
	p.configured = true
	p.mu.Unlock()
	clear(secret)
	return &pluginv1.ConfigApplyResult{Applied: true, SettingsRevision: request.GetSettingsRevision()}, nil
}

func (p *plugin) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go p.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func (p *plugin) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	p.mu.RLock()
	configured := p.configured && string(p.secret) == "secret-dsn-for-fixture"
	p.mu.RUnlock()
	if !configured {
		return &pluginv1.CallResponse{Code: "configuration_not_applied"}, nil
	}
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
	executable, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	service := &plugin{startMarker: executable + ".starts", marker: executable + ".crash"}
	if os.Getenv("LIAPOLDUS_TEST_SENTINEL") != "" {
		if err := os.WriteFile(service.startMarker+".inherited-environment", []byte{}, 0o600); err != nil {
			os.Exit(1)
		}
	}
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
	listener, err := transport.ListenInherited()
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
