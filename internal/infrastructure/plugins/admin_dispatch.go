package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

// RequestContext is the only HTTP information exposed to a plugin admin action.
// It deliberately contains no sockets, filesystem paths or raw credentials.
type RequestContext struct {
	Instance  string
	Namespace string
	Page      string
	Action    string
	Method    string
	RequestID string
	Actor     string
	Input     json.RawMessage
}

type ResponseAction struct {
	Status      int             `json:"status"`
	ContentType string          `json:"contentType"`
	Body        json.RawMessage `json:"body"`
}

type AdminDispatcher interface {
	Dispatch(context.Context, RequestContext) (ResponseAction, error)
}

type Dispatcher struct {
	instances map[string]AdminDispatcher
}

func NewDispatcher() *Dispatcher { return &Dispatcher{instances: make(map[string]AdminDispatcher)} }
func (d *Dispatcher) Register(instance string, handler AdminDispatcher) error {
	if instance == "" || handler == nil {
		return errors.New("plugin admin dispatcher registration is invalid")
	}
	if _, exists := d.instances[instance]; exists {
		return errors.New("plugin admin dispatcher instance already registered")
	}
	d.instances[instance] = handler
	return nil
}
func (d *Dispatcher) Dispatch(ctx context.Context, instance string, request RequestContext) (ResponseAction, error) {
	handler, ok := d.instances[instance]
	if !ok {
		return ResponseAction{}, errors.New("plugin admin instance is unavailable")
	}
	return handler.Dispatch(ctx, request)
}
