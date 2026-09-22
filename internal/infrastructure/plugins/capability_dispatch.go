package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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
	client      *Client
	allowed     map[string]struct{}
	active      chan struct{}
	rssLimit    uint64
	measureRSS  func() (uint64, error)
	stopProcess func()
	rssExceeded *atomic.Bool
}

func (c *CapabilityClient) setRSSLimit(limit uint64, measure func() (uint64, error), stop func(), exceeded *atomic.Bool) {
	c.rssLimit = limit
	c.measureRSS = measure
	c.stopProcess = stop
	c.rssExceeded = exceeded
}

func NewCapabilityClient(client *Client, maxConcurrentCalls int, capabilities ...string) (*CapabilityClient, error) {
	if client == nil {
		return nil, errors.New("plugin capability client is nil")
	}
	if maxConcurrentCalls < 1 {
		return nil, errors.New("plugin call limit is invalid")
	}
	allowed := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		allowed[capability] = struct{}{}
	}
	return &CapabilityClient{client: client, allowed: allowed, active: make(chan struct{}, maxConcurrentCalls)}, nil
}

func (c *CapabilityClient) HTTP(ctx context.Context, capability string, request HTTPRequest) (HTTPResponse, error) {
	if err := c.validateCapability(capability); err != nil {
		return HTTPResponse{}, err
	}
	if strings.TrimSpace(request.Method) == "" || strings.TrimSpace(request.Path) == "" {
		return HTTPResponse{}, errors.New("plugin http request is invalid")
	}
	var response HTTPResponse
	if err := c.callJSON(ctx, capability, request, &response); err != nil {
		return HTTPResponse{}, err
	}
	if response.Status < 100 || response.Status > 599 {
		return HTTPResponse{}, errors.New("plugin http response status is invalid")
	}
	return response, nil
}

func (c *CapabilityClient) L4(ctx context.Context, capability string, request L4Request) (L4Response, error) {
	if err := c.validateCapability(capability); err != nil {
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
	if err := c.callJSON(ctx, capability, request, &response); err != nil {
		return L4Response{}, err
	}
	return response, nil
}

func (c *CapabilityClient) DispatchIdentity(ctx context.Context, request IdentityRequest) (IdentityAction, error) {
	if err := c.validateCapability(request.Capability); err != nil {
		return IdentityAction{}, err
	}
	if strings.TrimSpace(request.Method) == "" || strings.TrimSpace(request.Path) == "" {
		return IdentityAction{}, errors.New("identity request is invalid")
	}
	var response IdentityAction
	if err := c.callJSON(ctx, request.Capability, request, &response); err != nil {
		return IdentityAction{}, err
	}
	if response.Status < 100 || response.Status > 599 {
		return IdentityAction{}, errors.New("identity response status is invalid")
	}
	return response, nil
}

func (c *CapabilityClient) callJSON(ctx context.Context, capability string, request, response any) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal plugin request: %w", err)
	}
	callContext, cancel := c.client.withDeadline(ctx)
	defer cancel()
	select {
	case c.active <- struct{}{}:
		defer func() { <-c.active }()
	case <-callContext.Done():
		return callContext.Err()
	}
	result, err := c.client.CallJSON(callContext, capability, payload)
	if err != nil {
		if c.enforceRSSLimit() {
			return ErrPluginResourceExhausted
		}
		return err
	}
	if c.enforceRSSLimit() {
		return ErrPluginResourceExhausted
	}
	return json.Unmarshal(result, response)
}

func (c *CapabilityClient) enforceRSSLimit() bool {
	if c.rssExceeded != nil && c.rssExceeded.Load() {
		return true
	}
	if c.measureRSS == nil || c.rssLimit == 0 {
		return false
	}
	resident, err := c.measureRSS()
	if err != nil || resident <= c.rssLimit {
		return false
	}
	if c.stopProcess != nil {
		if c.rssExceeded != nil {
			c.rssExceeded.Store(true)
		}
		c.stopProcess()
	}
	return true
}

func (c *CapabilityClient) validateCapability(capability string) error {
	if strings.TrimSpace(capability) == "" {
		return errors.New("plugin capability is not allowed")
	}
	if len(c.allowed) > 0 {
		if _, ok := c.allowed[capability]; !ok {
			return errors.New("plugin capability is not allowed")
		}
	}
	return nil
}
