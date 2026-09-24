package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
)

type grantPlugin struct {
	pluginv1.UnimplementedPluginServiceServer
	server *grpc.Server
}

var previousHandle string

func (*grantPlugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Name:            "forms",
		ProtocolVersion: pluginprotocol.ProtocolVersion,
		Capabilities:    []string{"forms.submit"},
		CapabilityDescriptors: []*pluginv1.CapabilityDescriptor{{
			Capability: "forms.submit",
			Modes:      []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_CALL},
		}},
	}, nil
}

func (*grantPlugin) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}

func (*grantPlugin) ConfigApply(context.Context, *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	return &pluginv1.ConfigApplyResult{Applied: true}, nil
}

func (p *grantPlugin) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go p.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func (*grantPlugin) Call(ctx context.Context, call *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	var httpRequest struct {
		Body []byte `json:"body"`
	}
	if err := json.Unmarshal(call.GetPayload(), &httpRequest); err != nil || len(call.GetGrants()) != 1 {
		return nil, fmt.Errorf("invalid fixture call")
	}
	broker, err := transport.DialGrantBrokerFromEnvironmentContext(ctx)
	if err != nil {
		return nil, err
	}
	defer broker.Close()
	var redeemed, wrongCapabilityDenied, wrongPurposeDenied, outOfScopeDenied, expiredHandleDenied bool
	if previousHandle != "" {
		_, err = broker.Redeem(ctx, call.GetCapability(), previousHandle, call.GetGrants()[0].GetPurpose(), "example.com")
		expiredHandleDenied = errors.Is(err, transport.ErrGrantRejected)
	} else {
		grant := call.GetGrants()[0]
		_, err = broker.Redeem(ctx, "forms.delete", grant.GetHandle(), grant.GetPurpose(), "example.com")
		wrongCapabilityDenied = errors.Is(err, transport.ErrGrantRejected)
		_, err = broker.Redeem(ctx, call.GetCapability(), grant.GetHandle(), "wrong-purpose", "example.com")
		wrongPurposeDenied = errors.Is(err, transport.ErrGrantRejected)
		_, err = broker.Redeem(ctx, call.GetCapability(), grant.GetHandle(), grant.GetPurpose(), "outside.example.net")
		outOfScopeDenied = errors.Is(err, transport.ErrGrantRejected)
		secret, redeemErr := broker.Redeem(ctx, call.GetCapability(), grant.GetHandle(), grant.GetPurpose(), "example.com")
		if redeemErr != nil {
			return nil, redeemErr
		}
		redeemed = len(secret) > 0
		previousHandle = grant.GetHandle()
	}
	response, err := json.Marshal(struct {
		Status int    `json:"status"`
		Body   []byte `json:"body"`
	}{Status: 200, Body: []byte(fmt.Sprintf(`{"redeemed":%t,"wrongCapabilityDenied":%t,"wrongPurposeDenied":%t,"outOfScopeDenied":%t,"expiredHandleDenied":%t,"secretInCallJSON":%t}`, redeemed, wrongCapabilityDenied, wrongPurposeDenied, outOfScopeDenied, expiredHandleDenied, strings.Contains(string(call.GetPayload()), "fixture-secret-material")))})
	if err != nil {
		return nil, err
	}
	return &pluginv1.CallResponse{Payload: response}, nil
}

func (*grantPlugin) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	for {
		if _, err := stream.Recv(); err != nil {
			return err
		}
	}
}

func run() error {
	listener, err := transport.ListenLoopback()
	if err != nil {
		return err
	}
	service := &grantPlugin{}
	service.server = transport.NewServer(service, transport.ServerOptions{})
	return service.server.Serve(listener)
}

func main() {
	if err := run(); err != nil && !strings.Contains(err.Error(), context.Canceled.Error()) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
