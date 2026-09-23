package network

import (
	"net/http"
	"sync"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type runtimeGeneration struct {
	graph    models.CompiledGraph
	geo      interfaces.GeoLookup
	proxies  map[string][]http.Handler
	limiters map[string]*rateLimiter
}

type WAFRuntime struct {
	mu                      sync.RWMutex
	generation              *runtimeGeneration
	providerProblem         models.Problem
	mtlsRequired            models.Problem
	bodyTooLarge            models.Problem
	headerTooLarge          models.Problem
	pluginResourceExhausted models.Problem
	pluginTimeout           models.Problem
	routeNotFound           models.Problem
	rateLimited             models.Problem
	retryAfterHeader        string
	requestIDHeader         string
	problemContentType      string
}

func NewWAFRuntime(graph models.CompiledGraph, lookup interfaces.GeoLookup, providerProblem models.Problem, bodyTooLarge models.Problem, problemContentType string) *WAFRuntime {
	return &WAFRuntime{
		generation:         prepareRuntimeGeneration(graph, lookup),
		providerProblem:    providerProblem,
		bodyTooLarge:       bodyTooLarge,
		problemContentType: problemContentType,
	}
}

func (runtime *WAFRuntime) BodyTooLargeProblem() models.Problem {
	return runtime.bodyTooLarge
}

func (runtime *WAFRuntime) SetMTLSRequiredProblem(problem models.Problem) {
	runtime.mtlsRequired = problem
}

func (runtime *WAFRuntime) MTLSRequiredProblem() models.Problem {
	return runtime.mtlsRequired
}

func (runtime *WAFRuntime) SetHeaderTooLargeProblem(problem models.Problem) {
	runtime.headerTooLarge = problem
}

func (runtime *WAFRuntime) HeaderTooLargeProblem() models.Problem {
	return runtime.headerTooLarge
}

func (runtime *WAFRuntime) SetRequestIDHeader(header string) {
	runtime.requestIDHeader = header
}

func (runtime *WAFRuntime) RequestIDHeader() string {
	return runtime.requestIDHeader
}

func (runtime *WAFRuntime) SetPluginResourceProblem(problem models.Problem) {
	runtime.pluginResourceExhausted = problem
}

func (runtime *WAFRuntime) PluginResourceProblem() models.Problem {
	return runtime.pluginResourceExhausted
}

func (runtime *WAFRuntime) SetPluginTimeoutProblem(problem models.Problem) {
	runtime.pluginTimeout = problem
}

func (runtime *WAFRuntime) PluginTimeoutProblem() models.Problem {
	return runtime.pluginTimeout
}

func (runtime *WAFRuntime) SetRouteNotFoundProblem(problem models.Problem) {
	runtime.routeNotFound = problem
}

func (runtime *WAFRuntime) RouteNotFoundProblem() models.Problem {
	return runtime.routeNotFound
}

func (runtime *WAFRuntime) SetRateLimitedProblem(problem models.Problem, retryAfterHeader string) {
	runtime.rateLimited = problem
	runtime.retryAfterHeader = retryAfterHeader
}

func (runtime *WAFRuntime) RateLimitedProblem() models.Problem {
	return runtime.rateLimited
}

func (runtime *WAFRuntime) RetryAfterHeader() string {
	return runtime.retryAfterHeader
}

func (runtime *WAFRuntime) Replace(graph models.CompiledGraph, lookup interfaces.GeoLookup) {
	next := prepareRuntimeGeneration(graph, lookup)
	runtime.mu.Lock()
	runtime.generation = next
	runtime.mu.Unlock()
}

func (runtime *WAFRuntime) Acquire() (*runtimeGeneration, func()) {
	runtime.mu.RLock()
	return runtime.generation, runtime.mu.RUnlock
}

func (runtime *WAFRuntime) Evaluate(generation *runtimeGeneration, name string, request models.WAFRequest) (models.WAFAction, bool) {
	policy, exists := generation.graph.WAFPolicies[name]
	if !exists {
		return models.WAFAction{Deny: &models.Deny{Status: http.StatusServiceUnavailable}}, true
	}
	return evaluateWAF(policy, request, generation.geo, runtime.providerProblem)
}

func (runtime *WAFRuntime) ProblemContentType() string {
	return runtime.problemContentType
}

func prepareRuntimeGeneration(graph models.CompiledGraph, lookup interfaces.GeoLookup) *runtimeGeneration {
	generation := &runtimeGeneration{
		graph:    graph,
		geo:      lookup,
		proxies:  make(map[string][]http.Handler, len(graph.Listeners)),
		limiters: make(map[string]*rateLimiter, len(graph.Listeners)),
	}
	for _, listener := range graph.Listeners {
		generation.proxies[listener.Address] = buildProxies(listener.Routes, graph.Upstreams)
		generation.limiters[listener.Address] = newRateLimiter(graph.RateLimits)
	}
	return generation
}
