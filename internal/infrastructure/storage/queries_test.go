package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"testing"
)

func TestCompiledQueriesPreserveSQL(t *testing.T) {
	cases := []struct {
		name    string
		queries map[string]string
		count   int
		digest  string
	}{
		{"historicalConfiguration", historicalConfigurationQueries(), 51, "4f86695cec22eb62633540807e99232031ee0e6a04031dbd260a853a74908df2"},
		{"configuration", configurationQueries(), 51, "3766c063a67928cd8355a72e583fee5edb318f47409252a34cf7e1dc782a3c24"},
		{"operation", operationQueries(), 10, "eba1b7d3b71c5e4d150d64e11ab9a072090ac56a60e6399bd604347d71cc005f"},
		{"trafficRollout", trafficRolloutQueries(), 31, "943263731b6b34eff7d72d716bc52e5f5e668c0f13ea4d7cefc212bb21e0adc4"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.queries) != tt.count {
				t.Fatalf("query count: got %d, want %d", len(tt.queries), tt.count)
			}
			keys := make([]string, 0, len(tt.queries))
			for key := range tt.queries {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			var source strings.Builder
			for _, key := range keys {
				source.WriteString(key)
				source.WriteByte(0)
				source.WriteString(tt.queries[key])
				source.WriteByte(0)
			}
			sum := sha256.Sum256([]byte(source.String()))
			if actual := hex.EncodeToString(sum[:]); actual != tt.digest {
				t.Fatalf("compiled SQL digest: got %s, want %s", actual, tt.digest)
			}
		})
	}
}
