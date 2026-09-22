package network

import (
	"net/http/httptest"
	"testing"

	"github.com/Liapoldus/core/internal/domain/models"
)

func TestRateLimiterReturnsRetryAfterOnSecondRequest(t *testing.T) {
	limiter := newRateLimiter(map[string]models.RateLimit{"api": {Requests: 1, Per: "1m", Burst: 1}})
	request := httptest.NewRequest("GET", "http://gateway.test/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	if retry, limited := limiter.Allow("api", request); limited || retry != 0 {
		t.Fatalf("first request limited=%v retry=%d", limited, retry)
	}
	if retry, limited := limiter.Allow("api", request); !limited || retry < 1 {
		t.Fatalf("second request limited=%v retry=%d", limited, retry)
	}
}
