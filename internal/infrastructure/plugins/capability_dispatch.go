package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// HTTPRequest is the bounded HTTP context sent to an HTTP capability. The
// gateway owns the listener, socket and credentials; none of those cross IPC.
type HTTPRequest struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	RequestID  string            `json:"requestId"`
	RemoteAddr string            `json:"remoteAddr,omitempty"`
}

type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// L4Request carries a bounded datagram/stream chunk. A plugin never receives
// a net.Conn or a listener address and cannot open a socket on the gateway's
// behalf.
type L4Request struct {
	Transport  string `json:"transport"` // tcp or udp
	Direction  string `json:"direction"` // request or response
	Payload    []byte `json:"payload,omitempty"`
	Connection string `json:"connectionId"`
}

type L4Response struct {
	Payload []byte `json:"payload,omitempty"`
	Drop    bool   `json:"drop,omitempty"`
}

type CapabilityClient struct {
	client *Client
}

func NewCapabilityClient(client *Client) (*CapabilityClient, error) {
	if client == nil {
		return nil, errors.New("plugin capability client is nil")
	}
	return &CapabilityClient{client: client}, nil
}

func (c *CapabilityClient) HTTP(ctx context.Context, capability string, request HTTPRequest) (HTTPResponse, error) {
	if err := validateCapability(capability, "http."); err != nil {
		return HTTPResponse{}, err
	}
	if strings.TrimSpace(request.Method) == "" || strings.TrimSpace(request.Path) == "" {
		return HTTPResponse{}, errors.New("plugin http request is invalid")
	}
	var response HTTPResponse
	if err := c.callJSON(ctx, "http.handle", capability, request, &response); err != nil {
		return HTTPResponse{}, err
	}
	if response.Status < 100 || response.Status > 599 {
		return HTTPResponse{}, errors.New("plugin http response status is invalid")
	}
	return response, nil
}

func (c *CapabilityClient) L4(ctx context.Context, capability string, request L4Request) (L4Response, error) {
	if err := validateCapability(capability, "tcp.", "udp."); err != nil {
		return L4Response{}, err
	}
	if request.Transport != "tcp" && request.Transport != "udp" {
		return L4Response{}, errors.New("plugin l4 transport is invalid")
	}
	if request.Direction != "request" && request.Direction != "response" {
		return L4Response{}, errors.New("plugin l4 direction is invalid")
	}
	if strings.TrimSpace(request.Connection) == "" {
		return L4Response{}, errors.New("plugin l4 connection id is required")
	}
	var response L4Response
	if err := c.callJSON(ctx, "l4.handle", capability, request, &response); err != nil {
		return L4Response{}, err
	}
	return response, nil
}

func (c *CapabilityClient) callJSON(ctx context.Context, method, capability string, request, response any) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal plugin request: %w", err)
	}
	result, err := c.client.CallRawJSON(ctx, method, capability, payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(result, response)
}

func validateCapability(capability string, prefixes ...string) error {
	for _, prefix := range prefixes {
		if strings.HasPrefix(capability, prefix) && len(capability) > len(prefix) {
			return nil
		}
	}
	return errors.New("plugin capability is not allowed")
}
