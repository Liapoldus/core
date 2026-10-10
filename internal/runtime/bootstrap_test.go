package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
)

func TestHostWaitReadyDoesNotConsumeExitResult(t *testing.T) {
	host, err := Start(context.Background(), config.BootstrapConfig{
		StatePath: filepath.Join(t.TempDir(), "missing", "core.sqlite"),
	}, RunOptions{WriteFailure: func(string, int, string, string) {}})
	if err != nil {
		t.Fatalf("start host: %v", err)
	}

	readyContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := host.WaitReady(readyContext); err == nil {
		t.Fatal("WaitReady succeeded after startup failure")
	}
	first := host.Wait()
	second := host.Wait()
	if first != second {
		t.Fatalf("Wait results = %d and %d", first, second)
	}
}
