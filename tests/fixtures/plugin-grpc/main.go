package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
	"os"
	"sync/atomic"
	"time"
)

type plugin struct {
	pluginv1.UnimplementedPluginServiceServer
	server      *grpc.Server
	activeCall  atomic.Int32
	maxCall     atomic.Int32
	crashMarker string
}

var memoryBlock []byte

type httpRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
}

func (*plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "forms", ProtocolVersion: "liapoldus.plugin.v1", Capabilities: []string{"forms.submit", "forms.concurrent", "forms.crash-once", "forms.memory", "tcp.echo"}}, nil
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
	if request.GetCapability() == "forms.memory" {
		memoryBlock = make([]byte, 128<<20)
		for index := 0; index < len(memoryBlock); index += 4096 {
			memoryBlock[index] = 1
		}
	}
	if request.GetCapability() == "forms.crash-once" {
		if _, err := os.Stat(p.crashMarker); os.IsNotExist(err) {
			if err := os.WriteFile(p.crashMarker, []byte{}, 0600); err != nil {
				return nil, err
			}
			os.Exit(23)
		}
	}
	if request.GetCapability() == "forms.concurrent" {
		current := p.activeCall.Add(1)
		defer p.activeCall.Add(-1)
		for observed := p.maxCall.Load(); current > observed; observed = p.maxCall.Load() {
			if p.maxCall.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		body, err := json.Marshal(map[string]int32{"maxConcurrent": p.maxCall.Load()})
		if err != nil {
			return nil, err
		}
		response, err := json.Marshal(struct {
			Status int    `json:"status"`
			Body   []byte `json:"body"`
		}{Status: 200, Body: body})
		if err != nil {
			return nil, err
		}
		return &pluginv1.CallResponse{Payload: response}, nil
	}
	if request.GetCapability() == "tcp.echo" {
		var input struct {
			Payload []byte `json:"payload"`
		}
		if err := json.Unmarshal(request.GetPayload(), &input); err != nil {
			return &pluginv1.CallResponse{Code: "invalid_request"}, nil
		}
		response, err := json.Marshal(struct {
			Payload []byte `json:"payload"`
		}{Payload: append([]byte("plugin:"), input.Payload...)})
		if err != nil {
			return nil, err
		}
		return &pluginv1.CallResponse{Payload: response}, nil
	}
	var input httpRequest
	if err := json.Unmarshal(request.GetPayload(), &input); err != nil {
		return &pluginv1.CallResponse{Code: "invalid_request"}, nil
	}
	_, authPresent := input.Headers["Authorization"]
	_, cookiePresent := input.Headers["Cookie"]
	body, err := json.Marshal(map[string]any{
		"method":               input.Method,
		"path":                 input.Path,
		"body":                 string(input.Body),
		"authorizationPresent": authPresent,
		"cookiePresent":        cookiePresent,
	})
	if err != nil {
		return nil, err
	}
	response, err := json.Marshal(struct {
		Status int    `json:"status"`
		Body   []byte `json:"body"`
	}{Status: 200, Body: body})
	if err != nil {
		return nil, err
	}
	return &pluginv1.CallResponse{Payload: response}, nil
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
	startMarker := os.Getenv("LIAPOLDUS_FIXTURE_START_MARKER")
	if startMarker != "" {
		file, err := os.OpenFile(startMarker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return
		}
		_, writeErr := file.WriteString("start\n")
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return
		}
	}
	listener, err := transport.ListenLoopback()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	service := &plugin{crashMarker: os.Getenv("LIAPOLDUS_FIXTURE_CRASH_MARKER")}
	service.server = transport.NewServer(service, transport.ServerOptions{})
	if err := service.server.Serve(listener); err != nil {
		return
	}
}
