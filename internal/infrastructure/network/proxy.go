package network

import (
	"errors"
	"hash/fnv"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/Liapoldus/core/internal/domain/models"
)

type proxyTarget struct {
	scheme   string
	host     string
	weight   int
	inflight atomic.Int32
}

type proxyUpstream struct {
	targets         []*proxyTarget
	counter         atomic.Uint64
	balance         models.BalanceMode
	hashSource      models.HashSource
	hashName        string
	retryAttempts   int
	retryConditions map[models.RetryCondition]struct{}
	hostMode        models.ProxyHostMode
	hostValue       string
	transport       http.RoundTripper
}

func buildProxies(routes []models.Route, upstreams map[string]models.Upstream) []http.Handler {
	handlers := make([]http.Handler, len(routes))
	for index, route := range routes {
		if route.Proxy == nil {
			continue
		}
		upstream, found := upstreams[route.Proxy.Upstream]
		if !found {
			continue
		}
		handlers[index] = newReverseProxy(upstream, route.Proxy)
	}
	return handlers
}

func newReverseProxy(upstream models.Upstream, proxy *models.ProxyTarget) http.Handler {
	pool := &proxyUpstream{
		balance:         upstream.Balance,
		hashSource:      upstream.Hash.Source,
		hashName:        upstream.Hash.Name,
		retryAttempts:   upstream.Retry.Attempts,
		retryConditions: map[models.RetryCondition]struct{}{},
		hostMode:        proxy.Host,
		hostValue:       proxy.HostValue,
		transport:       http.DefaultTransport,
	}
	for _, condition := range upstream.Retry.Conditions {
		pool.retryConditions[condition] = struct{}{}
	}
	if pool.balance == models.BalanceHash && pool.hashSource == 0 {
		pool.balance = models.BalanceRoundRobin
	}
	for _, candidate := range upstream.Targets {
		target, err := proxyTargetFrom(candidate)
		if err != nil {
			continue
		}
		pool.targets = append(pool.targets, target)
	}
	if len(pool.targets) == 0 {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Error(writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		})
	}
	return &httputil.ReverseProxy{
		Director:     pool.director,
		Transport:    pool,
		ErrorHandler: pool.errorHandler,
	}
}

func (pool *proxyUpstream) director(request *http.Request) {
	request.Header.Del("X-Forwarded-For")
	if peer := hostOf(request.RemoteAddr); peer != "" {
		request.Header.Set("X-Forwarded-For", peer)
	}
	request.Header.Set("X-Forwarded-Host", request.Host)
	proto := "http"
	if request.TLS != nil {
		proto = "https"
	}
	request.Header.Set("X-Forwarded-Proto", proto)
	if port := hostPort(request.Host); port != "" {
		request.Header.Set("X-Forwarded-Port", port)
	}
	if pool.hostMode == models.ProxyHostValue && pool.hostValue != "" {
		request.Host = pool.hostValue
	}
}

func (pool *proxyUpstream) RoundTrip(request *http.Request) (*http.Response, error) {
	limit := 1
	if pool.retryAttempts > 0 {
		limit = pool.retryAttempts + 1
	}
	if limit > len(pool.targets) {
		limit = len(pool.targets)
	}
	var last error
	for attempt := 0; attempt < limit; attempt++ {
		target := pool.next(request)
		out := request.Clone(request.Context())
		out.Header = request.Header.Clone()
		if out.Body != nil && out.GetBody != nil {
			body, err := out.GetBody()
			if err != nil {
				return nil, err
			}
			out.Body = body
		}
		out.URL = cloneURL(request.URL)
		out.URL.Scheme = target.scheme
		out.URL.Host = target.host
		if pool.hostMode == models.ProxyHostUpstream {
			out.Host = target.host
		}
		target.inflight.Add(1)
		response, err := pool.transport.RoundTrip(out)
		target.inflight.Add(-1)
		if err == nil {
			return response, nil
		}
		last = err
		if _, retryable := pool.retryConditions[models.RetryConnectFailure]; !retryable {
			return nil, err
		}
	}
	return nil, last
}

func (pool *proxyUpstream) errorHandler(writer http.ResponseWriter, request *http.Request, err error) {
	http.Error(writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
}

func (pool *proxyUpstream) next(request *http.Request) *proxyTarget {
	switch pool.balance {
	case models.BalanceLeastConnections:
		return pool.leastConnections()
	case models.BalanceHash:
		if target := pool.hashed(request); target != nil {
			return target
		}
	}
	return pool.roundRobin()
}

func (pool *proxyUpstream) roundRobin() *proxyTarget {
	total := 0
	for _, target := range pool.targets {
		total += target.weight
	}
	position := int(pool.counter.Add(1)-1) % total
	for _, target := range pool.targets {
		if position < target.weight {
			return target
		}
		position -= target.weight
	}
	return pool.targets[0]
}

func (pool *proxyUpstream) leastConnections() *proxyTarget {
	start := int(pool.counter.Add(1)-1) % len(pool.targets)
	picked := pool.targets[start]
	for offset := 1; offset < len(pool.targets); offset++ {
		candidate := pool.targets[(start+offset)%len(pool.targets)]
		if candidate.inflight.Load() < picked.inflight.Load() {
			picked = candidate
		}
	}
	return picked
}

func (pool *proxyUpstream) hashed(request *http.Request) *proxyTarget {
	var key string
	switch pool.hashSource {
	case models.HashSourceHeader:
		key = request.Header.Get(pool.hashName)
	case models.HashSourceCookie:
		if cookie, err := request.Cookie(pool.hashName); err == nil {
			key = cookie.Value
		}
	case models.HashSourceQuery:
		key = request.URL.Query().Get(pool.hashName)
	default:
		key = hostOf(request.RemoteAddr)
	}
	if key == "" {
		return nil
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return pool.weighted(int(hasher.Sum32()))
}

func (pool *proxyUpstream) weighted(value int) *proxyTarget {
	total := 0
	for _, target := range pool.targets {
		total += target.weight
	}
	position := value % total
	for _, target := range pool.targets {
		if position < target.weight {
			return target
		}
		position -= target.weight
	}
	return pool.targets[0]
}

func proxyTargetFrom(target models.UpstreamTarget) (*proxyTarget, error) {
	address := target.Address
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("unsupported scheme")
	}
	weight := target.Weight
	if weight < 1 {
		weight = 1
	}
	return &proxyTarget{scheme: parsed.Scheme, host: parsed.Host, weight: weight}, nil
}

func cloneURL(source *url.URL) *url.URL {
	if source == nil {
		return &url.URL{}
	}
	clone := *source
	return &clone
}

func hostPort(host string) string {
	if index := strings.LastIndex(host, ":"); index >= 0 {
		return host[index+1:]
	}
	return ""
}

func hostOf(address string) string {
	return strings.Split(address, ":")[0]
}
