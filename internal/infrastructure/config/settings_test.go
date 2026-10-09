package config

import (
	"encoding/json"
	"testing"
)

func TestSettingsRejectBootstrapOverridesAndDuplicateKeys(t *testing.T) {
	settings := Settings{
		Management:    ManagementSettings{Listen: "127.0.0.1:8080", TLS: TLSReferences{Certificate: "/mount/cert", Key: "/mount/key"}, MaxBodyBytes: 1048576, HeaderTimeout: "5s", RequestTimeout: "30s"},
		PluginControl: ControlSettings{Listen: "127.0.0.1:8081", PublicURL: "https://localhost:8081", TLS: TLSReferences{Certificate: "/mount/cert", Key: "/mount/key"}, ReplicaClientCA: "/mount/ca", ReplicaServerCA: "/mount/ca"},
		SecretRoot:    "/mount/secrets",
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSettings(raw); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{`"statePath":"/other"`, `"plugins":[]`, `"management":{}`} {
		invalid := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(","+extra+"}")...)
		if _, err := DecodeSettings(invalid); err == nil {
			t.Fatalf("accepted override/duplicate %s", extra)
		}
	}
	settings.Management.TLS.Key = "secret-value"
	if settings.Validate() == nil {
		t.Fatal("accepted a value instead of absolute mount reference")
	}
}
