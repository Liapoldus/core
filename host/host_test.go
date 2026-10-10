package host

import (
	"context"
	"errors"
	"testing"
)

func TestStartRejectsInvalidOptions(t *testing.T) {
	if _, err := Start(context.Background(), Options{StatePath: "relative.sqlite"}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("relative path error = %v", err)
	}
	if _, err := Start(nil, Options{StatePath: "/tmp/core.sqlite"}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nil context error = %v", err)
	}
}
