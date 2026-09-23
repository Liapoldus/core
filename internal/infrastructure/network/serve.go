package network

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/observability"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

type HTTPCapabilityDispatcher interface {
	HTTP(context.Context, string, plugins.HTTPRequest) (plugins.HTTPResponse, error)
}

type L4CapabilityDispatcher interface {
	L4(context.Context, string, plugins.L4Request) (plugins.L4Response, error)
}

// IdentityCapabilityDispatcher is the narrow HTTP boundary for auth policies.
// It deliberately accepts only the versioned identity request/action types.
type IdentityCapabilityDispatcher interface {
	DispatchIdentity(context.Context, plugins.IdentityRequest) (plugins.IdentityAction, error)
}

var connectionSequence uint64

func Serve(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, drainTimeout time.Duration, metrics ...*observability.Registry) error {
	return ServeWithCapabilities(parent, listeners, sites, upstreams, profiles, drainTimeout, nil, metrics...)
}

// ServeWithCapabilities wires already-handshaken plugin clients into HTTP
// routes. The map is an adapter boundary; it never exposes public sockets.
func ServeWithCapabilities(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, drainTimeout time.Duration, capabilities map[string]HTTPCapabilityDispatcher, metrics ...*observability.Registry) error {
	return ServeWithL4Capabilities(parent, listeners, sites, upstreams, profiles, drainTimeout, capabilities, nil, metrics...)
}

func ServeWithL4Capabilities(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, drainTimeout time.Duration, capabilities map[string]HTTPCapabilityDispatcher, l4Capabilities map[string]L4CapabilityDispatcher, metrics ...*observability.Registry) error {
	return serveWithRuntime(parent, listeners, sites, upstreams, profiles, nil, nil, nil, nil, drainTimeout, capabilities, l4Capabilities, metrics, nil, nil)
}

// ServeWithRateLimits is the full runtime entry point used by the CLI.
func ServeWithRateLimits(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, limits map[string]models.RateLimit, drainTimeout time.Duration, capabilities map[string]HTTPCapabilityDispatcher, l4Capabilities map[string]L4CapabilityDispatcher, metrics ...*observability.Registry) error {
	return serveWithRuntime(parent, listeners, sites, upstreams, profiles, limits, nil, nil, nil, drainTimeout, capabilities, l4Capabilities, metrics, nil, nil)
}

func ServeWithPolicies(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, limits map[string]models.RateLimit, policies map[string]models.WAFPolicy, drainTimeout time.Duration, capabilities map[string]HTTPCapabilityDispatcher, l4Capabilities map[string]L4CapabilityDispatcher, metrics ...*observability.Registry) error {
	return serveWithRuntime(parent, listeners, sites, upstreams, profiles, limits, policies, nil, nil, drainTimeout, capabilities, l4Capabilities, metrics, nil, nil)
}

func ServeWithDataProviders(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, limits map[string]models.RateLimit, policies map[string]models.WAFPolicy, providers interfaces.GeoLookup, drainTimeout time.Duration, metrics ...*observability.Registry) error {
	return serveWithRuntime(parent, listeners, sites, upstreams, profiles, limits, policies, nil, nil, drainTimeout, nil, nil, metrics, providers, nil)
}

func ServeWithWAFRuntime(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, limits map[string]models.RateLimit, runtime *WAFRuntime, drainTimeout time.Duration, metrics ...*observability.Registry) error {
	return serveWithRuntime(parent, listeners, sites, upstreams, profiles, limits, nil, nil, nil, drainTimeout, nil, nil, metrics, nil, runtime)
}

func ServeWithPluginRuntime(parent context.Context, graph models.CompiledGraph, runtime *WAFRuntime, drainTimeout time.Duration, capabilities map[string]HTTPCapabilityDispatcher, l4Capabilities map[string]L4CapabilityDispatcher, identity map[string]IdentityCapabilityDispatcher, metrics ...*observability.Registry) error {
	return serveWithRuntime(parent, graph.Listeners, graph.Sites, graph.Upstreams, graph.TLSProfiles,
		graph.RateLimits, graph.WAFPolicies, graph.AuthPolicies, identity, drainTimeout,
		capabilities, l4Capabilities, metrics, nil, runtime)
}

// ServeWithIdentityPolicies additionally wires compiled auth policies to
// already-handshaken identity plugin instances.
func ServeWithIdentityPolicies(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, limits map[string]models.RateLimit, policies map[string]models.WAFPolicy, authPolicies map[string]models.AuthPolicy, identity map[string]IdentityCapabilityDispatcher, drainTimeout time.Duration, capabilities map[string]HTTPCapabilityDispatcher, l4Capabilities map[string]L4CapabilityDispatcher, metrics ...*observability.Registry) error {
	return serveWithRuntime(parent, listeners, sites, upstreams, profiles, limits, policies, authPolicies, identity, drainTimeout, capabilities, l4Capabilities, metrics, nil, nil)
}

func serveWithRuntime(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, limits map[string]models.RateLimit, policies map[string]models.WAFPolicy, authPolicies map[string]models.AuthPolicy, identity map[string]IdentityCapabilityDispatcher, drainTimeout time.Duration, capabilities map[string]HTTPCapabilityDispatcher, l4Capabilities map[string]L4CapabilityDispatcher, metrics []*observability.Registry, geo interfaces.GeoLookup, wafRuntime *WAFRuntime) error {
	if wafRuntime == nil {
		wafRuntime = NewWAFRuntime(models.CompiledGraph{Listeners: listeners, Sites: sites, Upstreams: upstreams, TLSProfiles: profiles, RateLimits: limits, WAFPolicies: policies, AuthPolicies: authPolicies}, geo, models.Problem{}, models.Problem{}, "")
	}
	var started int
	errs := make(chan error, len(listeners))
	for _, listener := range listeners {
		started++
		go func(current models.Listener) {
			switch current.Type {
			case "tcp":
				errs <- serveTCP(parent, current, upstreams, drainTimeout, l4Capabilities)
			case "udp":
				errs <- serveUDP(parent, current, upstreams, drainTimeout, l4Capabilities)
			default:
				errs <- serveHTTP(parent, current, sites, upstreams, profiles, drainTimeout, firstRegistry(metrics), capabilities, limits, wafRuntime, authPolicies, identity)
			}
		}(listener)
	}
	if started == 0 {
		return errors.New("no http listener")
	}
	for i := 0; i < started; i++ {
		if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

func firstRegistry(registries []*observability.Registry) *observability.Registry {
	if len(registries) == 0 {
		return nil
	}
	return registries[0]
}

func runtimeListener(listeners []models.Listener, address string) (models.Listener, bool) {
	for _, listener := range listeners {
		if listener.Address == address {
			return listener, true
		}
	}
	return models.Listener{}, false
}

// serveTCP owns the public socket and relays each accepted stream to the first
// healthy configured target. L4 rules are deliberately evaluated before any
// plugin boundary; plugins never receive the public socket.
func serveTCP(parent context.Context, listener models.Listener, upstreams map[string]models.Upstream, drainTimeout time.Duration, capabilities map[string]L4CapabilityDispatcher) error {
	ln, err := net.Listen("tcp", listener.Address)
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() { <-parent.Done(); _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if parent.Err() != nil {
				return nil
			}
			return err
		}
		go relayTCP(parent, conn, listener.Rules, upstreams, drainTimeout, capabilities)
	}
}

func relayTCP(parent context.Context, client net.Conn, rules []models.Route, upstreams map[string]models.Upstream, drainTimeout time.Duration, capabilities map[string]L4CapabilityDispatcher) {
	defer client.Close()
	plugin := l4Plugin(rules, capabilities)
	if plugin.dispatcher != nil {
		connectionID := fmt.Sprintf("c-%d", atomic.AddUint64(&connectionSequence, 1))
		relayTCPPlugin(parent, client, plugin, connectionID)
		return
	}
	target := l4Target(rules, upstreams)
	if target == "" {
		return
	}
	dialer := net.Dialer{}
	server, err := dialer.DialContext(parent, "tcp", target)
	if err != nil {
		return
	}
	defer server.Close()
	connectionID := fmt.Sprintf("c-%d", atomic.AddUint64(&connectionSequence, 1))
	done := make(chan struct{}, 2)
	go func() { relayTCPDirection(parent, client, server, plugin, connectionID, "request"); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	select {
	case <-func() <-chan struct{} {
		finished := make(chan struct{})
		go func() { <-done; <-done; close(finished) }()
		return finished
	}():
	case <-parent.Done():
	}
	if drainTimeout > 0 {
		_ = client.SetDeadline(time.Now().Add(drainTimeout))
		_ = server.SetDeadline(time.Now().Add(drainTimeout))
	}
}

func relayTCPPlugin(ctx context.Context, client net.Conn, plugin l4RoutePlugin, connectionID string) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := client.Read(buffer)
		if n > 0 {
			result, dispatchErr := plugin.dispatcher.L4(ctx, plugin.capability, plugins.L4Request{Transport: "tcp", Direction: "request", Connection: connectionID, Payload: append([]byte(nil), buffer[:n]...)})
			if dispatchErr != nil || result.Drop {
				return
			}
			if _, writeErr := client.Write(result.Payload); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func serveUDP(parent context.Context, listener models.Listener, upstreams map[string]models.Upstream, drainTimeout time.Duration, capabilities map[string]L4CapabilityDispatcher) error {
	addr, err := net.ResolveUDPAddr("udp", listener.Address)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-parent.Done(); _ = conn.Close() }()
	buffer := make([]byte, 64*1024)
	for {
		n, source, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if parent.Err() != nil {
				return nil
			}
			return err
		}
		plugin := l4Plugin(listener.Rules, capabilities)
		target := l4Target(listener.Rules, upstreams)
		if target == "" && plugin.dispatcher == nil {
			continue
		}
		payload := append([]byte(nil), buffer[:n]...)
		go relayUDP(parent, conn, source, target, payload, drainTimeout, plugin)
	}
}

func relayUDP(parent context.Context, public *net.UDPConn, source *net.UDPAddr, target string, payload []byte, idle time.Duration, plugin l4RoutePlugin) {
	if plugin.dispatcher != nil && target == "" {
		result, err := plugin.dispatcher.L4(parent, plugin.capability, plugins.L4Request{Transport: "udp", Direction: "request", Connection: fmt.Sprintf("c-%d", atomic.AddUint64(&connectionSequence, 1)), Payload: payload})
		if err == nil && !result.Drop {
			_, _ = public.WriteToUDP(result.Payload, source)
		}
		return
	}
	addr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return
	}
	upstream, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return
	}
	defer upstream.Close()
	if plugin.dispatcher != nil {
		result, err := plugin.dispatcher.L4(parent, plugin.capability, plugins.L4Request{Transport: "udp", Direction: "request", Connection: fmt.Sprintf("c-%d", atomic.AddUint64(&connectionSequence, 1)), Payload: payload})
		if err != nil || result.Drop {
			return
		}
		payload = result.Payload
	}
	if idle <= 0 {
		idle = 30 * time.Second
	}
	_ = upstream.SetReadDeadline(time.Now().Add(idle))
	if _, err = upstream.Write(payload); err != nil {
		return
	}
	response := make([]byte, 64*1024)
	if n, err := upstream.Read(response); err == nil {
		_, _ = public.WriteToUDP(response[:n], source)
	}
	select {
	case <-parent.Done():
	default:
	}
}

type l4RoutePlugin struct {
	dispatcher L4CapabilityDispatcher
	capability string
}

type rateBucket struct {
	tokens  float64
	updated time.Time
}

type rateLimiter struct {
	limits  map[string]models.RateLimit
	mu      sync.Mutex
	buckets map[string]rateBucket
}

func newRateLimiter(limits map[string]models.RateLimit) *rateLimiter {
	return &rateLimiter{limits: limits, buckets: make(map[string]rateBucket)}
}

func (limiter *rateLimiter) Allow(name string, request *http.Request) (int, bool) {
	limit, ok := limiter.limits[name]
	if !ok {
		return 0, false
	}
	period, err := time.ParseDuration(limit.Per)
	if err != nil || period <= 0 {
		return 0, false
	}
	key := name + "|" + hostOf(request.RemoteAddr)
	now := time.Now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	bucket := limiter.buckets[key]
	capacity := limit.Burst
	if capacity <= 0 {
		capacity = limit.Requests
	}
	if capacity <= 0 {
		return 0, false
	}
	if bucket.updated.IsZero() {
		bucket = rateBucket{tokens: float64(capacity), updated: now}
	} else {
		refillPerSecond := float64(limit.Requests) / period.Seconds()
		bucket.tokens += now.Sub(bucket.updated).Seconds() * refillPerSecond
		if bucket.tokens > float64(capacity) {
			bucket.tokens = float64(capacity)
		}
		bucket.updated = now
	}
	if bucket.tokens < 1 {
		refillPerSecond := float64(limit.Requests) / period.Seconds()
		remaining := int(math.Ceil((1 - bucket.tokens) / refillPerSecond))
		if remaining < 1 {
			remaining = 1
		}
		limiter.buckets[key] = bucket
		return remaining, true
	}
	bucket.tokens--
	limiter.buckets[key] = bucket
	return 0, false
}

func l4Plugin(rules []models.Route, capabilities map[string]L4CapabilityDispatcher) l4RoutePlugin {
	for _, rule := range rules {
		if rule.Plugin != nil {
			return l4RoutePlugin{dispatcher: capabilities[rule.Plugin.Instance], capability: rule.Plugin.Capability}
		}
	}
	return l4RoutePlugin{}
}
func relayTCPDirection(ctx context.Context, source net.Conn, target net.Conn, plugin l4RoutePlugin, connectionID, direction string) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := source.Read(buffer)
		if n > 0 {
			payload := append([]byte(nil), buffer[:n]...)
			if plugin.dispatcher != nil {
				result, dispatchErr := plugin.dispatcher.L4(ctx, plugin.capability, plugins.L4Request{Transport: "tcp", Direction: direction, Connection: connectionID, Payload: payload})
				if dispatchErr != nil || result.Drop {
					return
				}
				payload = result.Payload
			}
			if _, writeErr := target.Write(payload); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func l4Target(rules []models.Route, upstreams map[string]models.Upstream) string {
	for _, rule := range rules {
		if rule.Deny != nil {
			return ""
		}
		if rule.Proxy == nil {
			continue
		}
		upstream, ok := upstreams[rule.Proxy.Upstream]
		if !ok || len(upstream.Targets) == 0 {
			continue
		}
		return upstream.Targets[0].Address
	}
	return ""
}

func serveHTTP(parent context.Context, listener models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, drainTimeout time.Duration, metrics *observability.Registry, capabilities map[string]HTTPCapabilityDispatcher, limits map[string]models.RateLimit, wafRuntime *WAFRuntime, authPolicies map[string]models.AuthPolicy, identity map[string]IdentityCapabilityDispatcher) error {
	baseHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		generation, release := wafRuntime.Acquire()
		defer release()
		activeListener, active := runtimeListener(generation.graph.Listeners, listener.Address)
		if !active {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var requestSize uint64
		if activeListener.Limits.Enabled {
			var tooLarge bool
			var bodyErr error
			requestSize, tooLarge, bodyErr = bufferRequestBody(request, activeListener.Limits.BodyBytes)
			if bodyErr != nil {
				var storageErr requestBodyStorageError
				if errors.As(bodyErr, &storageErr) {
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if tooLarge {
				problem := wafRuntime.BodyTooLargeProblem()
				if problem.Status == 0 {
					writer.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				problem.Instance = request.URL.Path
				problem.RequestID = request.Header.Get("X-Request-ID")
				writer.Header().Set("Content-Type", wafRuntime.ProblemContentType())
				writer.WriteHeader(problem.Status)
				_ = json.NewEncoder(writer).Encode(problem)
				return
			}
		} else if request.ContentLength > 0 {
			requestSize = uint64(request.ContentLength)
		}
		index, route, found := matchedRoute(request, activeListener.Routes)
		if !found {
			http.NotFound(writer, request)
			return
		}
		if route.Headers != nil {
			applyHeaderActions(request.Header, route.Headers.Request)
		}
		if route.Cache != nil {
			applyRouteCache(writer.Header(), route.Cache)
		}
		if route.CORS != nil {
			origin := request.Header.Get("Origin")
			originAllowed := len(route.CORS.Origins) == 0
			for _, candidate := range route.CORS.Origins {
				if candidate == "*" || candidate == origin {
					originAllowed = true
				}
			}
			preflight := request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != ""
			methodAllowed := len(route.CORS.Methods) == 0
			requestedMethod := request.Header.Get("Access-Control-Request-Method")
			for _, method := range route.CORS.Methods {
				if method == requestedMethod {
					methodAllowed = true
				}
			}
			if preflight && (!originAllowed || !methodAllowed) {
				preflight = false
				originAllowed = false
			}
			if origin != "" && originAllowed {
				writer.Header().Set("Access-Control-Allow-Origin", origin)
				writer.Header().Add("Vary", "Origin")
			}
			if preflight && len(route.CORS.Methods) > 0 {
				writer.Header().Set("Access-Control-Allow-Methods", strings.Join(route.CORS.Methods, ", "))
			}
			if preflight && len(route.CORS.Headers) > 0 {
				writer.Header().Set("Access-Control-Allow-Headers", strings.Join(route.CORS.Headers, ", "))
			}
			if !preflight && len(route.CORS.ExposeHeaders) > 0 {
				writer.Header().Set("Access-Control-Expose-Headers", strings.Join(route.CORS.ExposeHeaders, ", "))
			}
			if origin != "" && originAllowed && route.CORS.Credentials {
				writer.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if preflight {
				if requested := request.Header.Get("Access-Control-Request-Headers"); requested != "" && len(route.CORS.Headers) == 0 {
					writer.Header().Set("Access-Control-Allow-Headers", requested)
				}
				if route.CORS.MaxAge != "" {
					if duration, err := time.ParseDuration(route.CORS.MaxAge); err == nil {
						writer.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(duration/time.Second), 10))
					}
				}
				writer.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if route.Auth != "" {
			policy, configured := generation.graph.AuthPolicies[route.Auth]
			if len(generation.graph.AuthPolicies) == 0 {
				policy, configured = authPolicies[route.Auth]
			}
			dispatcher := identity[policy.Instance]
			if !configured || dispatcher == nil {
				writer.Header().Set("Content-Type", "application/problem+json")
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			headers := make(map[string]string, len(request.Header))
			for name, values := range request.Header {
				lower := strings.ToLower(name)
				if lower == "authorization" || lower == "cookie" || len(values) == 0 {
					continue
				}
				headers[name] = values[0]
			}
			action, err := dispatcher.DispatchIdentity(request.Context(), plugins.IdentityRequest{Instance: policy.Instance, Capability: policy.Capability, Method: request.Method, Path: request.URL.Path, Query: request.URL.RawQuery, Headers: headers, Body: body, RequestID: request.Header.Get("X-Request-ID")})
			if err != nil {
				if writePluginProblem(writer, request, wafRuntime, err) {
					return
				}
				writer.WriteHeader(http.StatusBadGateway)
				return
			}
			for name, value := range action.Headers {
				writer.Header().Set(name, value)
			}
			for _, cookie := range action.Cookies {
				writer.Header().Add("Set-Cookie", cookie)
			}
			writer.WriteHeader(action.Status)
			_, _ = writer.Write(action.Body)
			return
		}
		if route.WAF != "" {
			headers := make(map[string][]string, len(request.Header))
			for name, values := range request.Header {
				headers[name] = values
			}
			query := make(map[string][]string, len(request.URL.Query()))
			for name, values := range request.URL.Query() {
				query[name] = values
			}
			input := models.WAFRequest{Path: request.URL.Path, Method: request.Method, RemoteAddress: request.RemoteAddr, RequestSize: requestSize, Headers: headers, Query: query}
			if action, matched := wafRuntime.Evaluate(generation, route.WAF, input); matched {
				if action.Deny != nil {
					if action.Problem != nil {
						problem := *action.Problem
						problem.Instance = request.URL.Path
						problem.RequestID = request.Header.Get("X-Request-ID")
						writer.Header().Set("Content-Type", wafRuntime.ProblemContentType())
						writer.WriteHeader(problem.Status)
						_ = json.NewEncoder(writer).Encode(problem)
						return
					}
					writer.WriteHeader(action.Deny.Status)
					return
				}
				if action.Limit != "" {
					if retry, limited := generation.limiters[listener.Address].Allow(action.Limit, request); limited {
						writer.Header().Set("Retry-After", strconv.Itoa(retry))
						writer.WriteHeader(http.StatusTooManyRequests)
						return
					}
				}
			}
		}
		if route.RateLimit != "" {
			if retry, limited := generation.limiters[listener.Address].Allow(route.RateLimit, request); limited {
				writer.Header().Set("Retry-After", strconv.Itoa(retry))
				writer.WriteHeader(http.StatusTooManyRequests)
				return
			}
		}
		if route.Rewrite != nil && route.Rewrite.Pattern != nil {
			request.URL.Path = route.Rewrite.Pattern.ReplaceAllString(request.URL.Path, route.Rewrite.Replacement)
		}
		if route.Redirect != nil {
			serveRedirect(writer, request, *route.Redirect, route.Headers)
			return
		}
		if route.Deny != nil {
			if route.Deny.Code != "" {
				writer.Header().Set("Content-Type", "application/problem+json")
			}
			writer.WriteHeader(route.Deny.Status)
			return
		}
		if route.Plugin != nil {
			dispatcher := capabilities[route.Plugin.Instance]
			if dispatcher == nil {
				writer.Header().Set("Content-Type", "application/problem+json")
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			headers := make(map[string]string, len(request.Header))
			for name, values := range request.Header {
				lower := strings.ToLower(name)
				if lower == "authorization" || lower == "cookie" || len(values) == 0 {
					continue
				}
				headers[name] = values[0]
			}
			pluginResponse, err := dispatcher.HTTP(request.Context(), route.Plugin.Capability, plugins.HTTPRequest{Method: request.Method, Path: request.URL.Path, Query: request.URL.RawQuery, Headers: headers, Body: body, RequestID: request.Header.Get("X-Request-ID"), RemoteAddr: request.RemoteAddr})
			if err != nil {
				if writePluginProblem(writer, request, wafRuntime, err) {
					return
				}
				writer.Header().Set("Content-Type", "application/problem+json")
				writer.WriteHeader(http.StatusBadGateway)
				return
			}
			for name, value := range pluginResponse.Headers {
				if strings.EqualFold(name, "Set-Cookie") {
					writer.Header().Add(name, value)
				} else {
					writer.Header().Set(name, value)
				}
			}
			writer.WriteHeader(pluginResponse.Status)
			_, _ = writer.Write(pluginResponse.Body)
			return
		}
		if route.Proxy != nil {
			responseWriter := responseWriterWithActions(writer, route.Headers)
			if proxied := generation.proxies[listener.Address][index]; proxied != nil {
				proxied.ServeHTTP(responseWriter, request)
				return
			}
			http.NotFound(writer, request)
			return
		}
		site, exists := generation.graph.Sites[route.Site]
		if !exists || site.Source != models.SourceDirectory && site.Source != models.SourceRelease {
			http.NotFound(writer, request)
			return
		}
		if redirect, found := siteRedirect(request.URL.Path, site.Redirects); found {
			serveSiteRedirect(writer, redirect, responseHeaderActions(site.Headers, route.Headers))
			return
		}
		responseWriter := responseWriterWithActions(writer, site.Headers, route.Headers)
		if request.URL.Path == "" || strings.Contains(request.URL.Path, "/..") {
			http.NotFound(writer, request)
			return
		}
		requested := path.Clean(request.URL.Path)
		if requested == "/" {
			requested = "/" + site.Index
		}
		candidate := filepath.Join(site.Root, filepath.FromSlash(strings.TrimPrefix(requested, "/")))
		if !isWithin(site.Root, candidate) {
			http.NotFound(writer, request)
			return
		}
		info, err := os.Stat(candidate)
		if err != nil {
			if shouldSPAFallback(request, site, requested) {
				candidate = filepath.Join(site.Root, site.Index)
				info, err = os.Stat(candidate)
			}
		}
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		if info.IsDir() {
			candidate = filepath.Join(candidate, site.Index)
			indexInfo, err := os.Stat(candidate)
			if err != nil || indexInfo.IsDir() {
				http.NotFound(writer, request)
				return
			}
			info = indexInfo
		}
		if !isWithin(site.Root, candidate) || info.IsDir() {
			http.NotFound(writer, request)
			return
		}
		etag := fmt.Sprintf(`"lpg-r1-%x-%x"`, info.Size(), info.ModTime().UnixNano())
		responseWriter.Header().Set("ETag", etag)
		if request.Header.Get("If-None-Match") == etag {
			responseWriter.WriteHeader(http.StatusNotModified)
			return
		}
		applyStaticCache(responseWriter.Header(), site.Cache)
		http.ServeFile(responseWriter, request, candidate)
	})
	handler := gzipHandler(baseHandler)
	if metrics != nil {
		handler = instrumentHTTP(handler, metrics, listener.Address)
	}
	server := &http.Server{Addr: listener.Address, Handler: handler}
	var tlsConfig *tls.Config
	if listener.TLSProfile != "" {
		profile, ok := profiles[listener.TLSProfile]
		if !ok {
			return fmt.Errorf("tls profile %q not found", listener.TLSProfile)
		}
		loaded, err := loadTLSConfig(profile)
		if err != nil {
			return err
		}
		tlsConfig = loaded
		server.TLSConfig = tlsConfig
	}
	serveError := make(chan error, 1)
	go func() {
		if tlsConfig != nil {
			serveError <- server.ListenAndServeTLS("", "")
		} else {
			serveError <- server.ListenAndServe()
		}
	}()
	select {
	case err := <-serveError:
		return err
	case <-parent.Done():
		ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return err
		}
		return nil
	}
}

func writePluginProblem(writer http.ResponseWriter, request *http.Request, runtime *WAFRuntime, err error) bool {
	if runtime == nil {
		return false
	}
	var problem models.Problem
	switch {
	case errors.Is(err, plugins.ErrPluginResourceExhausted):
		problem = runtime.PluginResourceProblem()
	case errors.Is(err, plugins.ErrPluginTimeout):
		problem = runtime.PluginTimeoutProblem()
	default:
		return false
	}
	if problem.Status == 0 {
		return false
	}
	problem.Instance = request.URL.Path
	problem.RequestID = request.Header.Get("X-Request-ID")
	writer.Header().Set("Content-Type", runtime.ProblemContentType())
	writer.WriteHeader(problem.Status)
	_ = json.NewEncoder(writer).Encode(problem)
	return true
}

func evaluateWAF(policy models.WAFPolicy, request models.WAFRequest, provider interfaces.GeoLookup, providerProblem models.Problem) (models.WAFAction, bool) {
	for _, rule := range policy.Rules {
		result := evaluateWAFMatcher(rule.When, request, provider)
		if !result.determined {
			return providerFailureAction(rule, *result.failedProvider, providerProblem), true
		}
		if result.matched {
			return rule.Action, true
		}
	}
	return models.WAFAction{}, false
}

func bufferRequestBody(request *http.Request, limit uint64) (uint64, bool, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return 0, false, nil
	}
	original := request.Body
	defer original.Close()
	buffer, err := os.CreateTemp("", "")
	if err != nil {
		return 0, false, requestBodyStorageError{cause: err}
	}
	path := buffer.Name()
	removeBuffer := func() {
		_ = buffer.Close()
		_ = os.Remove(path)
	}
	size, err := io.Copy(buffer, io.LimitReader(original, int64(limit)+1))
	if err != nil {
		removeBuffer()
		return 0, false, err
	}
	requestSize := uint64(size)
	if requestSize > limit {
		removeBuffer()
		return requestSize, true, nil
	}
	if _, err := buffer.Seek(0, 0); err != nil {
		removeBuffer()
		return 0, false, requestBodyStorageError{cause: err}
	}
	request.Body = &temporaryRequestBody{File: buffer, path: path}
	request.ContentLength = size
	request.TransferEncoding = nil
	return requestSize, false, nil
}

type temporaryRequestBody struct {
	*os.File
	path string
}

func (body *temporaryRequestBody) Close() error {
	closeErr := body.File.Close()
	removeErr := os.Remove(body.path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

type requestBodyStorageError struct {
	cause error
}

func (failure requestBodyStorageError) Error() string {
	return failure.cause.Error()
}

func (failure requestBodyStorageError) Unwrap() error {
	return failure.cause
}

type wafMatcherResult struct {
	matched        bool
	determined     bool
	failedProvider *models.DataProvider
}

func evaluateWAFMatcher(matcher models.WAFMatcher, request models.WAFRequest, provider interfaces.GeoLookup) wafMatcherResult {
	if !matcher.MatchesRequestFields(request) {
		return wafMatcherResult{determined: true}
	}
	records := make(map[string]models.GeoRecord, 2)
	var failedProvider *models.DataProvider
	var address netip.Addr
	var addressParsed bool
	var addressValid bool
	lookupResults := make(map[models.DataProvider]geoLookupResult, 2)
	lookup := func(configuration models.DataProvider, providerName string) (models.GeoRecord, bool) {
		if result, exists := lookupResults[configuration]; exists {
			if result.failed != nil {
				if failedProvider == nil {
					failed := *result.failed
					failedProvider = &failed
				}
				return models.GeoRecord{}, false
			}
			records[providerName] = result.record
			return result.record, true
		}
		if !addressParsed {
			address, _ = netip.ParseAddr(remoteHost(request.RemoteAddress))
			addressValid = address.IsValid()
			addressParsed = true
		}
		if !addressValid || provider == nil {
			failed := configuration
			lookupResults[configuration] = geoLookupResult{failed: &failed}
			if failedProvider == nil {
				copy := configuration
				failedProvider = &copy
			}
			return models.GeoRecord{}, false
		}
		record, err := provider(configuration, address)
		if err != nil {
			failed := configuration
			lookupResults[configuration] = geoLookupResult{failed: &failed}
			if failedProvider == nil {
				copy := configuration
				failedProvider = &copy
			}
			return models.GeoRecord{}, false
		}
		lookupResults[configuration] = geoLookupResult{record: record}
		records[providerName] = record
		return record, true
	}
	if matcher.Geo != nil {
		if _, ok := lookup(matcher.Geo.Config, matcher.Geo.Provider); ok && !(models.WAFMatcher{Geo: matcher.Geo}).MatchesGeoRecords(records) {
			return wafMatcherResult{determined: true}
		}
	}
	if matcher.ASN != nil {
		if _, ok := lookup(matcher.ASN.Config, matcher.ASN.Provider); ok && !(models.WAFMatcher{ASN: matcher.ASN}).MatchesGeoRecords(records) {
			return wafMatcherResult{determined: true}
		}
	}
	result := wafMatcherResult{matched: true, determined: true}
	if failedProvider != nil {
		result.determined = false
		result.failedProvider = failedProvider
	}
	for _, child := range matcher.All {
		childResult := evaluateWAFMatcher(child, request, provider)
		if childResult.determined && !childResult.matched {
			return wafMatcherResult{determined: true}
		}
		if !childResult.determined && result.determined {
			result.determined = false
			result.failedProvider = childResult.failedProvider
		}
	}
	if matcher.Any != nil {
		matchedAny := false
		var anyFailure *models.DataProvider
		for _, child := range matcher.Any {
			childResult := evaluateWAFMatcher(child, request, provider)
			if !childResult.determined {
				if anyFailure == nil {
					anyFailure = childResult.failedProvider
				}
				continue
			}
			if childResult.matched {
				matchedAny = true
				break
			}
		}
		if !matchedAny {
			if anyFailure == nil {
				return wafMatcherResult{determined: true}
			}
			if result.determined {
				result.determined = false
				result.failedProvider = anyFailure
			}
		}
	}
	if matcher.Not != nil {
		childResult := evaluateWAFMatcher(*matcher.Not, request, provider)
		if childResult.determined && childResult.matched {
			return wafMatcherResult{determined: true}
		}
		if !childResult.determined && result.determined {
			result.determined = false
			result.failedProvider = childResult.failedProvider
		}
	}
	return result
}

type geoLookupResult struct {
	record models.GeoRecord
	failed *models.DataProvider
}

func providerFailureAction(rule models.WAFRule, provider models.DataProvider, problem models.Problem) models.WAFAction {
	onErrorAllow := rule.OnErrorAllow
	if !rule.OnErrorExplicit {
		onErrorAllow = provider.OnErrorAllow
	}
	if onErrorAllow {
		return models.WAFAction{Allow: true}
	}
	if problem.Status == 0 {
		return models.WAFAction{Deny: &models.Deny{Status: http.StatusForbidden}}
	}
	return models.WAFAction{Deny: &models.Deny{Status: problem.Status, Code: problem.Code}, Problem: &problem}
}

func remoteHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *metricsResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}
func (w *metricsResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func instrumentHTTP(next http.Handler, metrics *observability.Registry, listener string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &metricsResponseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		metrics.ObserveHTTP(listener, r.URL.Path, "", r.Method, fmt.Sprintf("%d", status), time.Since(started))
	})
}

func loadTLSConfig(profile models.TLSProfile) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: profile.Protocols}
	for _, certificate := range profile.Certificates {
		if certificate.Cert == "" || certificate.Key == "" {
			continue
		}
		pair, err := tls.LoadX509KeyPair(certificate.Cert, certificate.Key)
		if err != nil {
			return nil, fmt.Errorf("load tls certificate: %w", err)
		}
		config.Certificates = append(config.Certificates, pair)
	}
	if len(config.Certificates) == 0 {
		return nil, errors.New("tls profile has no certificate")
	}
	if profile.ClientAuth.Mode != "" {
		caBytes, err := os.ReadFile(profile.ClientAuth.CA)
		if err != nil {
			return nil, fmt.Errorf("load client ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, errors.New("invalid client ca")
		}
		config.ClientCAs = pool
		if profile.ClientAuth.Mode == "require" {
			config.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			config.ClientAuth = tls.VerifyClientCertIfGiven
		}
	}
	return config, nil
}

// LoadTLSConfig exposes the transport TLS adapter to the management
// presentation layer without leaking certificate loading into domain code.
func LoadTLSConfig(profile models.TLSProfile) (*tls.Config, error) {
	return loadTLSConfig(profile)
}

func shouldSPAFallback(request *http.Request, site models.Site, requested string) bool {
	if !site.SPA || (request.Method != http.MethodGet && request.Method != http.MethodHead) || strings.Contains(path.Base(requested), ".") {
		return false
	}
	return true
}

func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !acceptsGzip(request.Header.Get("Accept-Encoding")) || request.Header.Get("Range") != "" {
			next.ServeHTTP(writer, request)
			return
		}
		wrapped := &gzipResponseWriter{ResponseWriter: writer, request: request}
		defer wrapped.close()
		next.ServeHTTP(wrapped, request)
	})
}

func acceptsGzip(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	wildcard := -1.0
	for _, item := range strings.Split(value, ",") {
		parts := strings.Split(item, ";")
		coding := strings.ToLower(strings.TrimSpace(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			keyValue := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
			if len(keyValue) != 2 || strings.ToLower(strings.TrimSpace(keyValue[0])) != "q" {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(keyValue[1]), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				quality = 0
			} else {
				quality = parsed
			}
		}
		switch coding {
		case "gzip":
			return quality > 0
		case "*":
			wildcard = quality
		}
	}
	return wildcard > 0
}

type gzipResponseWriter struct {
	http.ResponseWriter
	request  *http.Request
	gzip     *gzip.Writer
	decided  bool
	compress bool
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.decided {
		return
	}
	w.decided = true
	contentType := w.Header().Get("Content-Type")
	w.compress = status >= 200 && status < 300 && (strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "json") || contentType == "") && w.Header().Get("Content-Encoding") == ""
	if w.compress {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if !w.decided {
		w.WriteHeader(http.StatusOK)
	}
	if w.compress {
		if w.gzip == nil {
			w.gzip = gzip.NewWriter(w.ResponseWriter)
		}
		return len(data), func() error { _, err := w.gzip.Write(data); return err }()
	}
	return w.ResponseWriter.Write(data)
}
func (w *gzipResponseWriter) close() {
	if w.gzip != nil {
		_ = w.gzip.Close()
	}
}

func applyStaticCache(header http.Header, cache *models.SiteCache) {
	if cache == nil || cache.Static.Visibility == "" {
		return
	}
	if cache.Static.Visibility == "no-store" {
		header.Set("Cache-Control", "no-store")
		return
	}
	value := cache.Static.Visibility
	if cache.Static.MaxAge != "" {
		if duration, err := time.ParseDuration(cache.Static.MaxAge); err == nil {
			value += fmt.Sprintf(", max-age=%d", int64(duration/time.Second))
		}
	}
	header.Set("Cache-Control", value)
}

func applyRouteCache(header http.Header, cache *models.RouteCache) {
	if cache == nil || cache.Visibility == "" {
		return
	}
	value := cache.Visibility
	if cache.MaxAge != "" {
		if duration, err := time.ParseDuration(cache.MaxAge); err == nil {
			value += fmt.Sprintf(", max-age=%d", int64(duration/time.Second))
		}
	}
	header.Set("Cache-Control", value)
}

func siteRedirect(requestPath string, redirects []models.SiteRedirect) (models.SiteRedirect, bool) {
	for _, redirect := range redirects {
		if redirect.From == requestPath {
			return redirect, true
		}
	}
	return models.SiteRedirect{}, false
}

func serveSiteRedirect(writer http.ResponseWriter, redirect models.SiteRedirect, actions []models.HeaderSet) {
	for _, action := range actions {
		applyHeaderActions(writer.Header(), action)
	}
	writer.Header().Set(redirect.LocationHeader, redirect.To)
	writer.WriteHeader(redirect.Status)
}

func responseHeaderActions(sets ...*models.HeaderActions) []models.HeaderSet {
	var actions []models.HeaderSet
	for _, set := range sets {
		if set != nil && headerSetNonEmpty(set.Response) {
			actions = append(actions, set.Response)
		}
	}
	return actions
}

func responseWriterWithActions(writer http.ResponseWriter, sets ...*models.HeaderActions) http.ResponseWriter {
	actions := responseHeaderActions(sets...)
	if len(actions) == 0 {
		return writer
	}
	return &headerActionsWriter{ResponseWriter: writer, actions: actions}
}

func matchedRoute(request *http.Request, routes []models.Route) (int, models.Route, bool) {
	for index, route := range routes {
		if !route.When.MatchesRequest(request.Host, request.Method, request.URL.Path, request.Header, request.URL.Query()) {
			continue
		}
		return index, route, true
	}
	return -1, models.Route{}, false
}

func applyHeaderActions(header http.Header, actions models.HeaderSet) {
	for name, value := range actions.Set {
		header.Set(name, value)
	}
	for name, value := range actions.SetIfAbsent {
		if header.Get(name) == "" {
			header.Set(name, value)
		}
	}
	for _, name := range actions.Delete {
		header.Del(name)
	}
}

func headerSetNonEmpty(actions models.HeaderSet) bool {
	return len(actions.Set) > 0 || len(actions.SetIfAbsent) > 0 || len(actions.Delete) > 0
}

func serveRedirect(writer http.ResponseWriter, request *http.Request, redirect models.RouteRedirect, headers *models.HeaderActions) {
	status := redirect.Status
	if status == 0 {
		status = 308
	}
	if headers != nil {
		applyHeaderActions(writer.Header(), headers.Response)
	}
	writer.Header().Set("Location", redirectLocation(request, redirect))
	writer.WriteHeader(status)
}

func redirectLocation(request *http.Request, redirect models.RouteRedirect) string {
	scheme := redirect.Scheme
	if scheme == "" {
		scheme = "http"
		if request.TLS != nil {
			scheme = "https"
		}
	}
	host := redirect.Host
	if host == "" {
		host = request.Host
	}
	target := &url.URL{Scheme: scheme, Host: host, Path: request.URL.Path}
	if redirect.Path != "" {
		target.Path = redirect.Path
	}
	if redirect.PreserveQuery && request.URL.RawQuery != "" {
		target.RawQuery = request.URL.RawQuery
	}
	return target.String()
}

type headerActionsWriter struct {
	http.ResponseWriter
	actions []models.HeaderSet
	wrote   bool
}

func (writer *headerActionsWriter) WriteHeader(status int) {
	if !writer.wrote {
		writer.wrote = true
		for _, actions := range writer.actions {
			applyHeaderActions(writer.Header(), actions)
		}
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *headerActionsWriter) Write(body []byte) (int, error) {
	if !writer.wrote {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *headerActionsWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *headerActionsWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking not supported")
	}
	return hijacker.Hijack()
}

func isWithin(root, candidate string) bool {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	resolvedCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
