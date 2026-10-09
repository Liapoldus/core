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
		{"configuration", configurationQueries(), 49, "93080a7d072d4bcee1b7b66048fa305fc33302fc7c966f40051be21b5e61bbb3"},
		{"operation", operationQueries(), 9, "b307862290234f828eee15bea13e29d038cbfb9bc707d311f61aeed14ebab3e3"},
		{"trafficRollout", trafficRolloutQueries(), 31, "1c9708587a028a098e0554170aab6c52693d4e56274516b226bff3650c4ce05b"},
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
			if hex.EncodeToString(sum[:]) != tt.digest {
				t.Fatal("compiled SQL differs from pre-migration baseline")
			}
		})
	}
}
