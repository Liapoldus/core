package plugins

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// IdentityRequest is the deliberately small boundary exposed to an identity
// capability. Raw credentials, sockets and filesystem paths never cross it.
type IdentityRequest struct {
	Instance   string
	Capability string
	Method     string
	Path       string
	Query      string
	Headers    map[string]string
	Body       []byte
	RequestID  string
}

// IdentityAction is the only response an identity capability may return.
// Gateway remains responsible for writing the HTTP response and applying
// cookies, identity claims and headers.
type IdentityAction struct {
	Status  int
	Headers map[string]string
	Cookies []string
	Body    []byte
	Subject string
	Claims  map[string]string
}

type IdentityDispatcher interface {
	DispatchIdentity(context.Context, IdentityRequest) (IdentityAction, error)
}

// IdentityBoundary validates both sides of the versioned identity contract.
type IdentityBoundary struct{ instances map[string]IdentityDispatcher }

func NewIdentityBoundary() *IdentityBoundary {
	return &IdentityBoundary{instances: make(map[string]IdentityDispatcher)}
}

func (b *IdentityBoundary) Register(instance string, handler IdentityDispatcher) error {
	if strings.TrimSpace(instance) == "" || handler == nil {
		return errors.New("identity plugin registration is invalid")
	}
	if _, exists := b.instances[instance]; exists {
		return errors.New("identity plugin instance already registered")
	}
	b.instances[instance] = handler
	return nil
}

func (b *IdentityBoundary) Dispatch(ctx context.Context, request IdentityRequest) (IdentityAction, error) {
	handler, ok := b.instances[request.Instance]
	if !ok {
		return IdentityAction{}, errors.New("identity plugin instance is unavailable")
	}
	if strings.TrimSpace(request.Capability) == "" || request.Method == "" {
		return IdentityAction{}, errors.New("identity request is invalid")
	}
	action, err := handler.DispatchIdentity(ctx, request)
	if err != nil {
		return IdentityAction{}, err
	}
	if action.Status < http.StatusContinue || action.Status > 599 {
		return IdentityAction{}, errors.New("identity response status is invalid")
	}
	return action, nil
}
