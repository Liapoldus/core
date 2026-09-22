package plugins

import (
	"context"
	"testing"
)

type identityTestPlugin struct{}

func (identityTestPlugin) DispatchIdentity(_ context.Context, request IdentityRequest) (IdentityAction, error) {
	return IdentityAction{Status: 302, Headers: map[string]string{"Location": request.Path}}, nil
}

func TestIdentityBoundaryExposesOnlyTypedContextAndActions(t *testing.T) {
	boundary := NewIdentityBoundary()
	if err := boundary.Register("identity", identityTestPlugin{}); err != nil {
		t.Fatal(err)
	}
	action, err := boundary.Dispatch(context.Background(), IdentityRequest{Instance: "identity", Capability: "identity.client.authenticate", Method: "GET", Path: "/login"})
	if err != nil {
		t.Fatal(err)
	}
	if action.Status != 302 || action.Headers["Location"] != "/login" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestIdentityBoundaryRejectsInvalidAction(t *testing.T) {
	boundary := NewIdentityBoundary()
	if err := boundary.Register("identity", invalidIdentityPlugin{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boundary.Dispatch(context.Background(), IdentityRequest{Instance: "identity", Capability: "identity.token.validate", Method: "POST"}); err == nil {
		t.Fatal("expected invalid response error")
	}
}

type invalidIdentityPlugin struct{}

func (invalidIdentityPlugin) DispatchIdentity(context.Context, IdentityRequest) (IdentityAction, error) {
	return IdentityAction{Status: 0}, nil
}
