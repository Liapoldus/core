package network

import "testing"

func TestAcceptsGzipHonorsQualityValues(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"gzip", true},
		{"br, gzip;q=0.5", true},
		{"gzip;q=0", false},
		{"gzip; q=0.0, *;q=0.8", false},
		{"br, *;q=0.4", true},
		{"br", false},
	}
	for _, test := range tests {
		if got := acceptsGzip(test.input); got != test.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", test.input, got, test.want)
		}
	}
}
