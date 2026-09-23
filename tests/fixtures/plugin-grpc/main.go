package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
	Context map[string]string `json:"context"`
	WAF     *struct {
		Headers     map[string][]string `json:"headers"`
		Query       map[string][]string `json:"query"`
		RequestSize uint64              `json:"requestSize"`
	} `json:"waf"`
}

func (*plugin) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	if delay := os.Getenv("LIAPOLDUS_FIXTURE_MANIFEST_DELAY"); delay != "" {
		wait, err := time.ParseDuration(delay)
		if err != nil {
			return nil, err
		}
		time.Sleep(wait)
	}
	capabilities := []string{"forms.submit", "forms.concurrent", "forms.crash-once", "forms.memory", "forms.slow", "peer.session", "tcp.echo"}
	if configured := os.Getenv("LIAPOLDUS_FIXTURE_MANIFEST_CAPABILITIES"); configured != "" {
		capabilities = strings.Split(configured, ",")
	}
	name := os.Getenv("LIAPOLDUS_FIXTURE_MANIFEST_NAME")
	if name == "" {
		name = "forms"
	}
	return &pluginv1.Manifest{Name: name, ProtocolVersion: "liapoldus.plugin.v1", Capabilities: capabilities}, nil
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

func (p *plugin) Call(ctx context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request.GetCapability() == "forms.slow" {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
			return &pluginv1.CallResponse{Payload: []byte(`{"status":200}`)}, nil
		}
	}
	if request.GetCapability() == "forms.memory" {
		memoryBlock = make([]byte, 257<<20)
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
	if input.WAF != nil {
		_, authPresent := input.Headers["Authorization"]
		_, cookiePresent := input.Headers["Cookie"]
		response, _ := json.Marshal(struct {
			Continue bool `json:"continue"`
			Response struct {
				Status  int               `json:"status"`
				Headers map[string]string `json:"headers"`
				Cookies []string          `json:"cookies"`
				Body    []byte            `json:"body"`
			} `json:"response"`
		}{Continue: false, Response: struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Cookies []string          `json:"cookies"`
			Body    []byte            `json:"body"`
		}{Status: 403, Headers: map[string]string{"X-Policy-Decision": "plugin", "X-Credentials-Forwarded": fmt.Sprintf("%t", authPresent || cookiePresent)}, Body: []byte("request denied by configured capability")}})
		return &pluginv1.CallResponse{Payload: response}, nil
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
	opened := false
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
			if opened || body.Open.GetConnectionId() == "" || !json.Valid(body.Open.GetContextJson()) {
				return status.Error(codes.InvalidArgument, "invalid stream open")
			}
			if body.Open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_TCP && body.Open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_UDP {
				return status.Error(codes.InvalidArgument, "invalid stream transport")
			}
			opened = true
		case *pluginv1.StreamMessage_Data:
			if !opened || body.Data.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST {
				return status.Error(codes.InvalidArgument, "invalid stream data")
			}
			response := append([]byte("stream:"), body.Data.GetPayload()...)
			if err := stream.Send(&pluginv1.StreamMessage{
				Capability: message.GetCapability(),
				Body: &pluginv1.StreamMessage_Data{Data: &pluginv1.StreamData{
					Payload:   response,
					Direction: pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE,
				}},
			}); err != nil {
				return err
			}
		case *pluginv1.StreamMessage_Close:
			if !opened {
				return status.Error(codes.InvalidArgument, "stream closed before open")
			}
			if err := stream.Send(&pluginv1.StreamMessage{
				Capability: message.GetCapability(),
				Body:       &pluginv1.StreamMessage_Close{Close: body.Close},
			}); err != nil {
				return err
			}
			return nil
		default:
			return status.Error(codes.InvalidArgument, "unsupported stream message")
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
