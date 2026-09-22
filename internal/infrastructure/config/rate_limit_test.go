package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompileGatewayCollectsRateLimits(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "gateway.yaml")
	contents := "registry:\n  path: " + filepath.Join(directory, "registry") + "\nrateLimits:\n  public: { key: source-ip, requests: 1, per: 1m, burst: 1 }\nlisteners:\n  web:\n    type: http\n    address: 127.0.0.1:0\n    routes: []\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	graph, err := CompileGateway(path)
	if err != nil {
		t.Fatal(err)
	}
	limit, ok := graph.RateLimits["public"]
	if !ok || limit.Key != "source-ip" || limit.Requests != 1 || limit.Burst != 1 || limit.Per != "1m" {
		t.Fatalf("compiled rate limit = %#v", limit)
	}
}
