package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestStorageDefinitionsPreserveValues(t *testing.T) {
	cases := []struct {
		name   string
		value  any
		digest string
	}{
		{"accessDefinitions", accessDefinitions(), "d67e782ec644168405b8d2eba75580e0406a2d26407c4f40001654af33a2df01"},
		{"auditDefinitions", auditDefinitions(), "d4fbc2fb03e5e02f582291e320a25315ba3aa1c633ff682a2d3d67269930ed77"},
		{"configurationDefinitions", ConfigurationDefinitions(), "9cb5a837e3126c08530bcee0cf8a4bd09f5f904f3afc9070230d66cdfd9a3385"},
		{"linkPolicyDefinitions", linkPolicyDefinitions(), "ac40431ad5a8278eae55a3dddeaec22aff6ca19f6082a299eeac6851be16140a"},
		{"operationDefinitions", operationDefinitions(), "4c110ceee3eb782ea01637653776ecda3029b32bd649802218ea2a9333602351"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(encoded)
			if hex.EncodeToString(sum[:]) != tt.digest {
				t.Fatal("storage definitions differ from pre-migration baseline")
			}
		})
	}
}
