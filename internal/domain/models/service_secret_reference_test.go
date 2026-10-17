package models

import "testing"

func TestServiceSecretReferenceValidate(t *testing.T) {
	valid := ServiceSecretReference{
		ID:        "ssr:v1:0123456789abcdef0123456789abcdef",
		ServiceID: "server",
		Purpose:   "server.tls.certificate",
		Label:     "Public certificate",
		State:     ServiceSecretReferenceActive,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid safe metadata rejected: %v", err)
	}

	for name, mutate := range map[string]func(*ServiceSecretReference){
		"raw path id":         func(value *ServiceSecretReference) { value.ID = "file:/private/key.pem" },
		"malformed opaque id": func(value *ServiceSecretReference) { value.ID = "ssr:v1:short" },
		"invalid service":     func(value *ServiceSecretReference) { value.ServiceID = "../server" },
		"invalid purpose":     func(value *ServiceSecretReference) { value.Purpose = "tls private key" },
		"secret-like label":   func(value *ServiceSecretReference) { value.Label = "private\nkey" },
		"unknown state":       func(value *ServiceSecretReference) { value.State = "enabled" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid secret-reference metadata accepted")
			}
		})
	}
}
