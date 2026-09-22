package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

func newHasher() hash.Hash { return sha256.New() }

func CompileGateway(path string) (models.CompiledGraph, error) {
	compiled, loaded, err := supervise(path)
	if err != nil {
		return models.CompiledGraph{}, err
	}
	graph, err := buildCompiled(path, loaded, compiled)
	if err != nil {
		return models.CompiledGraph{}, err
	}
	if err := validateReferences(graph); err != nil {
		return models.CompiledGraph{}, err
	}
	return graph, nil
}

func validateReferences(graph models.CompiledGraph) error {
	for _, listener := range graph.Listeners {
		for _, route := range listener.Routes {
			if route.Site != "" {
				if _, exists := graph.Sites[route.Site]; !exists {
					return ErrUndefinedSite
				}
			}
			if route.Proxy != nil {
				if _, exists := graph.Upstreams[route.Proxy.Upstream]; !exists {
					return ErrUndefinedUpstream
				}
			}
			if route.Auth != "" {
				if _, exists := graph.AuthPolicies[route.Auth]; !exists {
					return ErrUndefinedAuthPolicy
				}
			}
		}
	}
	catalog, err := errorCatalog()
	if err != nil {
		return err
	}
	if err := validateSiteSemantics(graph, catalog); err != nil {
		return err
	}
	return validateManagementSemantics(graph, catalog)
}

func validateSiteSemantics(graph models.CompiledGraph, catalog ErrorCatalog) error {
	words, err := loadContractFile()
	if err != nil {
		return err
	}
	for name, site := range graph.Sites {
		if site.DefaultLocale == "" {
			continue
		}
		listed := false
		for _, locale := range site.Locales {
			if locale == site.DefaultLocale {
				listed = true
				break
			}
		}
		if !listed {
			return &CompileProblem{
				Problem: catalog.Problem(words.Codes.SiteInvalid, words.Semantics.DefaultLocaleMustBeListed, name),
			}
		}
	}
	return nil
}

func validateManagementSemantics(graph models.CompiledGraph, catalog ErrorCatalog) error {
	loaded, err := loadContractFile()
	if err != nil {
		return err
	}
	words := loaded.Runtime
	management := graph.Management
	address := management.Listener.Address
	if address == "" {
		return nil
	}
	remote := !isLoopback(address)
	if remote {
		if management.Listener.TLSProfile == "" {
			return &CompileProblem{
				Problem: catalog.Problem(loaded.Codes.ManagementTLSRequired, loaded.Semantics.ManagementNonLoopbackTLS, "management.listener"),
			}
		}
		profile, exists := graph.TLSProfiles[management.Listener.TLSProfile]
		if !exists || profile.ClientAuth.Mode != words.ClientAuth.Require {
			return &CompileProblem{
				Problem: catalog.Problem(loaded.Codes.ManagementMTLSRequired, loaded.Semantics.ManagementNonLoopbackMTLS, "management.listener"),
			}
		}
	}
	if management.StaticToken != "" {
		if remote {
			return &CompileProblem{
				Problem: catalog.Problem(loaded.Codes.ConfigInvalid, loaded.Semantics.ManagementStaticTokenNonLoopback, "management.staticToken"),
			}
		}
		if len(management.ServiceAccounts) > 0 {
			return &CompileProblem{
				Problem: catalog.Problem(loaded.Codes.ConfigInvalid, loaded.Semantics.ManagementStaticTokenExclusive, "management.staticToken"),
			}
		}
	}
	if remote && len(management.ServiceAccounts) == 0 {
		return &CompileProblem{
			Problem: catalog.Problem(loaded.Codes.ConfigInvalid, loaded.Semantics.ManagementRemoteRequiresAccount, "management.serviceAccounts"),
		}
	}
	return nil
}

func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var errorCatalogOnce = sync.OnceValues(loadErrorCatalogOnce)

func loadErrorCatalogOnce() (ErrorCatalog, error) {
	return LoadErrorCatalog()
}

func errorCatalog() (ErrorCatalog, error) {
	return errorCatalogOnce()
}

func buildCompiled(path string, loaded contractFile, compiled *graph) (models.CompiledGraph, error) {
	graph := models.CompiledGraph{
		Revision: models.Revision{
			Value:  path,
			Digest: hex.EncodeToString(compiled.hasher.Sum(nil)),
		},
		Sites:        map[string]models.Site{},
		Secrets:      map[string]models.Secret{},
		Upstreams:    map[string]models.Upstream{},
		TLSProfiles:  map[string]models.TLSProfile{},
		RateLimits:   map[string]models.RateLimit{},
		WAFPolicies:  map[string]models.WAFPolicy{},
		AuthPolicies: map[string]models.AuthPolicy{},
	}
	base := filepath.Dir(path)
	layout, err := LoadRegistryLayout()
	if err != nil {
		return models.CompiledGraph{}, err
	}
	registryRoot := collectRegistryRoot(compiled.documents, loaded, base)
	for _, document := range compiled.documents {
		for index := 0; index < len(document.Content); index += 2 {
			key, node := document.Content[index], document.Content[index+1]
			switch key.Value {
			case loaded.Listeners:
				listeners, err := collectListeners(node, loaded.Runtime)
				if err != nil {
					return models.CompiledGraph{}, err
				}
				graph.Listeners = append(graph.Listeners, listeners...)
			case loaded.Sites:
				if err := collectSites(node, loaded.Runtime, base, registryRoot, layout, graph.Sites); err != nil {
					return models.CompiledGraph{}, err
				}
			case loaded.Upstreams:
				if err := collectUpstreams(node, loaded.Runtime, graph.Upstreams); err != nil {
					return models.CompiledGraph{}, err
				}
			case loaded.RateLimits:
				if err := collectRateLimits(node, loaded.Runtime, graph.RateLimits); err != nil {
					return models.CompiledGraph{}, err
				}
			case loaded.WAFPolicies:
				if err := collectWAFPolicies(node, loaded.Runtime, graph.WAFPolicies); err != nil {
					return models.CompiledGraph{}, err
				}
			case loaded.AuthPolicies:
				collectAuthPolicies(node, graph.AuthPolicies)
			case loaded.Runtime.Section.TLSProfiles:
				if err := collectTLSProfiles(node, loaded.Runtime, graph.TLSProfiles); err != nil {
					return models.CompiledGraph{}, err
				}
			case loaded.Runtime.Section.Management:
				graph.Management = collectManagement(node, loaded.Runtime)
			case loaded.Runtime.Section.Logging, loaded.Runtime.Section.Metrics, loaded.Runtime.Section.Tracing:
				graph.Observability = collectObservability(graph.Observability, key.Value, node, loaded.Runtime)
			}
		}
	}
	for name, value := range compiled.secrets {
		graph.Secrets[name] = models.Secret{Value: value}
	}
	return graph, nil
}

func collectAuthPolicies(node *yaml.Node, policies map[string]models.AuthPolicy) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, body := node.Content[i].Value, node.Content[i+1]
		plugin := mappingNode(body, "plugin")
		if plugin == nil {
			continue
		}
		instance, _ := fieldValue(plugin, "instance")
		capability, _ := fieldValue(plugin, "capability")
		if instance != "" && capability != "" {
			policies[name] = models.AuthPolicy{Instance: instance, Capability: capability}
		}
	}
}

func collectWAFPolicies(node *yaml.Node, _ runtimeWords, policies map[string]models.WAFPolicy) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, body := node.Content[i].Value, node.Content[i+1]
		policy := models.WAFPolicy{}
		rules := mappingNode(body, "rules")
		if rules == nil || rules.Kind != yaml.SequenceNode {
			continue
		}
		for _, rn := range rules.Content {
			when := mappingNode(rn, "when")
			then := mappingNode(rn, "then")
			if when == nil || then == nil {
				continue
			}
			rule := models.WAFRule{When: models.PathMatcher{}}
			if path := mappingNode(when, "path"); path != nil {
				if path.Kind == yaml.ScalarNode {
					rule.When.Exact = path.Value
				} else if p := mappingNode(path, "prefix"); p != nil {
					rule.When.Prefixes = []string{p.Value}
				} else if p := mappingNode(path, "exact"); p != nil {
					rule.When.Exact = p.Value
				}
			}
			if mappingNode(then, "allow") != nil {
				rule.Action.Allow = true
			} else if d := mappingNode(then, "deny"); d != nil {
				action := &models.Deny{Status: 403, Code: "forbidden"}
				if s := mappingNode(d, "status"); s != nil {
					if n, e := strconv.Atoi(s.Value); e == nil {
						action.Status = n
					}
				}
				if c := mappingNode(d, "code"); c != nil {
					action.Code = c.Value
				}
				rule.Action.Deny = action
			}
			policy.Rules = append(policy.Rules, rule)
		}
		policies[name] = policy
	}
	return nil
}

func collectRateLimits(node *yaml.Node, words runtimeWords, limits map[string]models.RateLimit) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, body := node.Content[index], node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		requests, _ := fieldValue(body, words.RateLimit.Requests)
		burst, _ := fieldValue(body, words.RateLimit.Burst)
		per, _ := fieldValue(body, words.RateLimit.Per)
		key, _ := fieldValue(body, words.RateLimit.Key)
		parsedRequests, err := strconv.Atoi(requests)
		if err != nil || parsedRequests < 1 {
			return fmt.Errorf("invalid rate limit %q requests", name.Value)
		}
		parsedBurst, err := strconv.Atoi(burst)
		if err != nil || parsedBurst < 1 {
			return fmt.Errorf("invalid rate limit %q burst", name.Value)
		}
		if _, err := time.ParseDuration(per); err != nil {
			return fmt.Errorf("invalid rate limit %q period: %w", name.Value, err)
		}
		limits[name.Value] = models.RateLimit{Key: key, Requests: parsedRequests, Per: per, Burst: parsedBurst}
	}
	return nil
}

func collectRegistryRoot(documents []*yaml.Node, loaded contractFile, base string) string {
	pattern := ""
	for _, document := range documents {
		node := mappingNode(document, loaded.Registry.Section)
		if node == nil || node.Kind != yaml.MappingNode {
			continue
		}
		if value, ok := fieldValue(node, loaded.Registry.Path); ok && value != "" {
			pattern = value
		}
	}
	if pattern == "" {
		return ""
	}
	if !filepath.IsAbs(pattern) {
		return filepath.Join(base, pattern)
	}
	return pattern
}

func collectListeners(node *yaml.Node, words runtimeWords) ([]models.Listener, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nil
	}
	var listeners []models.Listener
	for index := 0; index < len(node.Content); index += 2 {
		body := node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		listener := models.Listener{}
		typ, _ := fieldValue(body, words.Listener.Type)
		listener.Type = typ
		listener.IsHTTP = typ == words.HTTP
		listener.Address, _ = fieldValue(body, words.Listener.Address)
		listener.TLSProfile, _ = fieldValue(body, words.Listener.TLSProfile)
		routeField := words.Listener.Routes
		if !listener.IsHTTP {
			routeField = words.Listener.Rules
		}
		routes, err := collectRoutes(mappingNode(body, routeField), words)
		if err != nil {
			return nil, err
		}
		listener.Routes = routes
		listener.Rules = routes
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func collectRoutes(node *yaml.Node, words runtimeWords) ([]models.Route, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var routes []models.Route
	for _, routeNode := range node.Content {
		if routeNode.Kind != yaml.MappingNode {
			continue
		}
		var route models.Route
		if when := mappingNode(routeNode, words.Route.When); when != nil {
			if pathNode := mappingNode(when, words.Route.Path); pathNode != nil {
				matcher, err := compilePathMatcher(pathNode, words)
				if err != nil {
					return nil, err
				}
				route.When = matcher
			}
		}
		if then := mappingNode(routeNode, words.Route.Then); then != nil {
			route.Site, _ = fieldValue(then, words.Route.Site)
			route.Proxy = compileProxyTarget(mappingNode(then, words.Route.Proxy), words)
			route.Redirect = compileRedirect(mappingNode(then, words.Route.Redirect), words)
			rewrite, err := compileRewrite(mappingNode(then, words.Route.Rewrite), words)
			if err != nil {
				return nil, err
			}
			route.Rewrite = rewrite
			route.Headers = compileHeaderActions(mappingNode(then, words.Route.Headers), words)
			route.Deny = compileDeny(mappingNode(then, words.Route.Deny), words)
			route.Plugin = compilePlugin(mappingNode(then, words.Route.Plugin), words)
			route.Auth, _ = fieldValue(then, words.Route.Auth)
			route.WAF, _ = fieldValue(then, words.Route.WAF)
			route.RateLimit, _ = fieldValue(then, words.Route.RateLimit)
			route.CORS = compileRouteCORS(mappingNode(then, words.Route.CORS), words)
			route.Cache = compileRouteCache(mappingNode(then, words.Route.Cache), words)
		}
		if terminals := countTerminalActions(route); terminals > 1 {
			return nil, ErrMultipleTerminalActions
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func compileRouteCache(node *yaml.Node, words runtimeWords) *models.RouteCache {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	c := &models.RouteCache{}
	c.Visibility, _ = fieldValue(node, words.SiteCache.Visibility)
	c.MaxAge, _ = fieldValue(node, words.SiteCache.MaxAge)
	return c
}

func compileRouteCORS(node *yaml.Node, words runtimeWords) *models.RouteCORS {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	c := &models.RouteCORS{}
	if origins := mappingNode(node, "origins"); origins != nil && origins.Kind == yaml.SequenceNode {
		for _, n := range origins.Content {
			if n.Kind == yaml.ScalarNode {
				c.Origins = append(c.Origins, n.Value)
			}
		}
	}
	if methods := mappingNode(node, "methods"); methods != nil && methods.Kind == yaml.SequenceNode {
		for _, n := range methods.Content {
			if n.Kind == yaml.ScalarNode {
				c.Methods = append(c.Methods, n.Value)
			}
		}
	}
	if headers := mappingNode(node, "headers"); headers != nil && headers.Kind == yaml.SequenceNode {
		for _, n := range headers.Content {
			if n.Kind == yaml.ScalarNode {
				c.Headers = append(c.Headers, n.Value)
			}
		}
	}
	if expose := mappingNode(node, "exposeHeaders"); expose != nil && expose.Kind == yaml.SequenceNode {
		for _, n := range expose.Content {
			if n.Kind == yaml.ScalarNode {
				c.ExposeHeaders = append(c.ExposeHeaders, n.Value)
			}
		}
	}
	if value, ok := fieldValue(node, "credentials"); ok {
		c.Credentials, _ = strconv.ParseBool(value)
	}
	c.MaxAge, _ = fieldValue(node, words.SiteCache.MaxAge)
	return c
}

func compileDeny(node *yaml.Node, words runtimeWords) *models.Deny {
	if node == nil {
		return nil
	}
	d := &models.Deny{Status: 403, Code: "forbidden"}
	if status, ok := fieldValue(node, words.Deny.Status); ok {
		if parsed, err := strconv.Atoi(status); err == nil {
			d.Status = parsed
		}
	}
	if code, ok := fieldValue(node, words.Deny.Code); ok && code != "" {
		d.Code = code
	}
	return d
}

func countTerminalActions(route models.Route) int {
	terminals := 0
	if route.Site != "" {
		terminals++
	}
	if route.Proxy != nil {
		terminals++
	}
	if route.Redirect != nil {
		terminals++
	}
	if route.Deny != nil {
		terminals++
	}
	if route.Plugin != nil {
		terminals++
	}
	return terminals
}

func compilePlugin(node *yaml.Node, words runtimeWords) *models.PluginTarget {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	instance, _ := fieldValue(node, words.Plugin.Instance)
	capability, _ := fieldValue(node, words.Plugin.Capability)
	if instance == "" || capability == "" {
		return nil
	}
	return &models.PluginTarget{Instance: instance, Capability: capability}
}

func compileProxyTarget(node *yaml.Node, words runtimeWords) *models.ProxyTarget {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			return nil
		}
		return &models.ProxyTarget{Upstream: node.Value, Host: models.ProxyHostPreserve, HostValue: ""}
	case yaml.MappingNode:
		target := models.ProxyTarget{Host: models.ProxyHostPreserve}
		target.Upstream, _ = fieldValue(node, words.Proxy.Upstream)
		if host, ok := fieldValue(node, words.Proxy.Host); ok {
			switch host {
			case words.Proxy.HostUpstream:
				target.Host = models.ProxyHostUpstream
			case words.Proxy.HostPreserve:
				target.Host = models.ProxyHostPreserve
			default:
				target.Host = models.ProxyHostValue
				target.HostValue = host
			}
		}
		return &target
	}
	return nil
}

func compileRedirect(node *yaml.Node, words runtimeWords) *models.RouteRedirect {
	if node == nil {
		return nil
	}
	redirect := models.RouteRedirect{PreserveQuery: true, Status: 308}
	redirect.Scheme, _ = fieldValue(node, words.Redirect.Scheme)
	redirect.Host, _ = fieldValue(node, words.Redirect.Host)
	redirect.Path, _ = fieldValue(node, words.Redirect.Path)
	if preserve, ok := fieldValue(node, words.Redirect.PreserveQuery); ok {
		if parsed, err := strconv.ParseBool(preserve); err == nil {
			redirect.PreserveQuery = parsed
		}
	}
	if status, ok := fieldValue(node, words.Redirect.Status); ok {
		if parsed, err := strconv.Atoi(status); err == nil {
			redirect.Status = parsed
		}
	}
	if redirect.Status == 0 {
		redirect.Status = 308
	}
	return &redirect
}

func compileRewrite(node *yaml.Node, words runtimeWords) (*models.Rewrite, error) {
	if node == nil {
		return nil, nil
	}
	raw, _ := fieldValue(node, words.Rewrite.Regex)
	replacement, _ := fieldValue(node, words.Rewrite.Replacement)
	if raw == "" {
		return nil, nil
	}
	compiled, err := regexp.Compile(raw)
	if err != nil {
		return nil, err
	}
	return &models.Rewrite{Pattern: compiled, Replacement: replacement}, nil
}

func compileHeaderActions(node *yaml.Node, words runtimeWords) *models.HeaderActions {
	if node == nil {
		return nil
	}
	actions := models.HeaderActions{}
	if request := mappingNode(node, words.Headers.Request); request != nil {
		actions.Request = compileHeaderSet(request, words)
	}
	if response := mappingNode(node, words.Headers.Response); response != nil {
		actions.Response = compileHeaderSet(response, words)
	}
	return &actions
}

func compileHeaderSet(node *yaml.Node, words runtimeWords) models.HeaderSet {
	set := models.HeaderSet{}
	if raw := mappingNode(node, words.Headers.Set); raw != nil && raw.Kind == yaml.MappingNode {
		set.Set = stringPairs(raw)
	}
	if raw := mappingNode(node, words.Headers.SetIfAbsent); raw != nil && raw.Kind == yaml.MappingNode {
		set.SetIfAbsent = stringPairs(raw)
	}
	if raw := mappingNode(node, words.Headers.Delete); raw != nil && raw.Kind == yaml.SequenceNode {
		for _, item := range raw.Content {
			if item.Kind == yaml.ScalarNode && item.Value != "" {
				set.Delete = append(set.Delete, item.Value)
			}
		}
	}
	return set
}

func stringPairs(node *yaml.Node) map[string]string {
	values := map[string]string{}
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		if value.Kind == yaml.ScalarNode {
			values[key.Value] = value.Value
		}
	}
	return values
}

func collectUpstreams(node *yaml.Node, words runtimeWords, upstreams map[string]models.Upstream) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, body := node.Content[index], node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		upstream, err := compileUpstream(body, words)
		if err != nil {
			return err
		}
		upstreams[name.Value] = upstream
	}
	return nil
}

func compileUpstream(node *yaml.Node, words runtimeWords) (models.Upstream, error) {
	upstream := models.Upstream{Balance: models.BalanceRoundRobin}
	switch balance, _ := fieldValue(node, words.UpstreamConfig.Balance); balance {
	case words.UpstreamConfig.BalanceLeastConnections:
		upstream.Balance = models.BalanceLeastConnections
	case words.UpstreamConfig.BalanceHash:
		upstream.Balance = models.BalanceHash
	}
	if hashNode := mappingNode(node, words.UpstreamConfig.Hash); hashNode != nil {
		if source, ok := fieldValue(hashNode, words.UpstreamConfig.HashSource); ok {
			switch source {
			case words.UpstreamConfig.HashSourceHeader:
				upstream.Hash.Source = models.HashSourceHeader
			case words.UpstreamConfig.HashSourceCookie:
				upstream.Hash.Source = models.HashSourceCookie
			case words.UpstreamConfig.HashSourceQuery:
				upstream.Hash.Source = models.HashSourceQuery
			default:
				upstream.Hash.Source = models.HashSourceIP
			}
		}
		upstream.Hash.Name, _ = fieldValue(hashNode, words.UpstreamConfig.HashName)
	}
	if retryNode := mappingNode(node, words.UpstreamConfig.Retry); retryNode != nil {
		if attempts, ok := fieldValue(retryNode, words.UpstreamConfig.RetryAttempts); ok {
			if parsed, err := strconv.Atoi(attempts); err == nil {
				upstream.Retry.Attempts = parsed
			}
		}
		if on := mappingNode(retryNode, words.UpstreamConfig.RetryOn); on != nil && on.Kind == yaml.SequenceNode {
			for _, item := range on.Content {
				if item.Kind != yaml.ScalarNode {
					continue
				}
				upstream.Retry.Conditions = append(upstream.Retry.Conditions, retryCondition(item.Value, words))
			}
		}
	}
	targets := mappingNode(node, words.UpstreamConfig.Targets)
	if targets == nil || targets.Kind != yaml.SequenceNode {
		return upstream, nil
	}
	for _, item := range targets.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		address, ok := fieldValue(item, words.UpstreamConfig.TargetAddress)
		if !ok || address == "" {
			return models.Upstream{}, ErrInvalidTarget
		}
		if err := validateTargetAddress(address); err != nil {
			return models.Upstream{}, err
		}
		weight := 1
		if raw, known := fieldValue(item, words.UpstreamConfig.TargetWeight); known {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 {
				weight = parsed
			}
		}
		upstream.Targets = append(upstream.Targets, models.UpstreamTarget{Address: address, Weight: weight})
	}
	return upstream, nil
}

func retryCondition(word string, words runtimeWords) models.RetryCondition {
	switch word {
	case words.UpstreamConfig.RetryTimeout:
		return models.RetryTimeout
	case words.UpstreamConfig.RetryStatus502:
		return models.RetryStatus502
	case words.UpstreamConfig.RetryStatus503:
		return models.RetryStatus503
	case words.UpstreamConfig.RetryStatus504:
		return models.RetryStatus504
	default:
		return models.RetryConnectFailure
	}
}

func validateTargetAddress(address string) error {
	if strings.HasPrefix(address, "://") {
		return ErrInvalidTarget
	}
	if !strings.Contains(address, "://") {
		return nil
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return ErrInvalidTarget
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrInvalidTarget
	}
	return nil
}

func compilePathMatcher(node *yaml.Node, words runtimeWords) (models.PathMatcher, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		return models.PathMatcher{Prefixes: []string{node.Value}}, nil
	case yaml.SequenceNode:
		var prefixes []string
		for _, item := range node.Content {
			if item.Kind == yaml.ScalarNode && item.Value != "" {
				prefixes = append(prefixes, item.Value)
			}
		}
		return models.PathMatcher{Prefixes: prefixes}, nil
	case yaml.MappingNode:
		if prefix, ok := fieldValue(node, words.Route.Prefix); ok {
			return models.PathMatcher{Prefixes: []string{prefix}}, nil
		}
		if exact, ok := fieldValue(node, words.Route.Exact); ok {
			return models.PathMatcher{Exact: exact}, nil
		}
		if raw, ok := fieldValue(node, words.Route.Regex); ok {
			anchored := "^(?:" + raw + ")$"
			compiled, err := regexp.Compile(anchored)
			if err != nil {
				return models.PathMatcher{}, err
			}
			return models.PathMatcher{Regex: compiled}, nil
		}
	}
	return models.PathMatcher{}, nil
}

func collectSites(node *yaml.Node, words runtimeWords, base, registryRoot string, layout models.RegistryLayout, sites map[string]models.Site) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, body := node.Content[index], node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		site, err := compileSite(body, words, base, registryRoot, layout)
		if err != nil {
			return err
		}
		sites[name.Value] = site
	}
	return nil
}

func compileSite(node *yaml.Node, words runtimeWords, base, registryRoot string, layout models.RegistryLayout) (models.Site, error) {
	site := models.Site{Index: words.Site.IndexDefault}
	source := mappingNode(node, words.Site.Source)
	if source == nil {
		return site, nil
	}
	if kind, ok := fieldValue(source, words.Site.Type); ok {
		switch kind {
		case words.Directory:
			site.Source = models.SourceDirectory
		case words.Release:
			site.Source = models.SourceRelease
			if slug, ok := fieldValue(source, words.Site.Slug); ok && slug != "" && registryRoot != "" {
				site.Root = filepath.Join(registryRoot, layout.Sites, slug, layout.Current)
			}
		}
	}
	if site.Source == models.SourceDirectory {
		if root, ok := fieldValue(source, words.Site.Root); ok && root != "" {
			site.Root = root
			if !filepath.IsAbs(root) {
				site.Root = filepath.Join(base, root)
			}
		}
	}
	if site.Root == "" {
		return site, nil
	}
	manifest := readManifest(filepath.Join(site.Root, words.Site.ManifestFileName))
	if manifest != nil {
		applyManifest(manifest, words, &site)
	}
	return site, nil
}

func applyManifest(manifest *yaml.Node, words runtimeWords, site *models.Site) {
	if index, ok := fieldValue(manifest, words.Site.Index); ok && index != "" {
		site.Index = index
	}
	if spa, ok := fieldValue(manifest, words.Site.SPA); ok {
		if parsed, err := strconv.ParseBool(spa); err == nil {
			site.SPA = parsed
		}
	}
	if defaultLocale, ok := fieldValue(manifest, words.Site.DefaultLocale); ok {
		site.DefaultLocale = defaultLocale
	}
	if locales := mappingNode(manifest, words.Site.Locales); locales != nil && locales.Kind == yaml.SequenceNode {
		for _, item := range locales.Content {
			if item.Kind == yaml.ScalarNode && item.Value != "" {
				site.Locales = append(site.Locales, item.Value)
			}
		}
	}
	site.Redirects = collectSiteRedirects(mappingNode(manifest, words.Site.Redirects), words)
	site.Headers = compileHeaderActions(mappingNode(manifest, words.Site.Headers), words)
	site.Cache = collectSiteCache(mappingNode(manifest, words.Site.Cache), words)
}

func collectSiteRedirects(node *yaml.Node, words runtimeWords) []models.SiteRedirect {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var redirects []models.SiteRedirect
	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		redirect := models.SiteRedirect{Status: 308, LocationHeader: words.SiteRedirect.Location}
		redirect.From, _ = fieldValue(item, words.SiteRedirect.From)
		redirect.To, _ = fieldValue(item, words.SiteRedirect.To)
		if status, ok := fieldValue(item, words.SiteRedirect.Status); ok {
			if parsed, err := strconv.Atoi(status); err == nil && parsed != 0 {
				redirect.Status = parsed
			}
		}
		redirects = append(redirects, redirect)
	}
	return redirects
}

func collectSiteCache(node *yaml.Node, words runtimeWords) *models.SiteCache {
	if node == nil {
		return nil
	}
	cache := models.SiteCache{}
	if static := mappingNode(node, words.SiteCache.Static); static != nil {
		cache.Static.Visibility, _ = fieldValue(static, words.SiteCache.Visibility)
		cache.Static.MaxAge, _ = fieldValue(static, words.SiteCache.MaxAge)
	}
	return &cache
}

func collectTLSProfiles(node *yaml.Node, words runtimeWords, profiles map[string]models.TLSProfile) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, body := node.Content[index], node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		profile := models.TLSProfile{}
		if certificates := mappingNode(body, words.TLSProfile.Certificates); certificates != nil && certificates.Kind == yaml.SequenceNode {
			for _, item := range certificates.Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				certificate := models.TLSCertificate{}
				certificate.Issuer, _ = fieldValue(item, words.TLSProfile.Issuer)
				certificate.Cert, _ = fieldValue(item, words.TLSProfile.Cert)
				certificate.Key, _ = fieldValue(item, words.TLSProfile.Key)
				if domains := mappingNode(item, words.TLSProfile.Domains); domains != nil && domains.Kind == yaml.SequenceNode {
					for _, domain := range domains.Content {
						if domain.Kind == yaml.ScalarNode && domain.Value != "" {
							certificate.Domains = append(certificate.Domains, domain.Value)
						}
					}
				}
				profile.Certificates = append(profile.Certificates, certificate)
			}
		}
		if protocols := mappingNode(body, words.TLSProfile.Protocols); protocols != nil && protocols.Kind == yaml.SequenceNode {
			for _, item := range protocols.Content {
				if item.Kind == yaml.ScalarNode && item.Value != "" {
					profile.Protocols = append(profile.Protocols, item.Value)
				}
			}
		}
		if clientAuth := mappingNode(body, words.TLSProfile.ClientAuth); clientAuth != nil {
			profile.ClientAuth.Mode, _ = fieldValue(clientAuth, words.ClientAuth.Mode)
			profile.ClientAuth.CA, _ = fieldValue(clientAuth, words.ClientAuth.CA)
		}
		profiles[name.Value] = profile
	}
	return nil
}

func collectManagement(node *yaml.Node, words runtimeWords) models.Management {
	management := models.Management{}
	if listener := mappingNode(node, words.Management.Listener); listener != nil {
		management.Listener.Address, _ = fieldValue(listener, words.Management.Address)
		management.Listener.TLSProfile, _ = fieldValue(listener, words.Management.TLSProfile)
	}
	management.StaticToken, _ = fieldValue(node, words.Management.StaticToken)
	if accounts := mappingNode(node, words.Management.ServiceAccounts); accounts != nil && accounts.Kind == yaml.SequenceNode {
		for _, item := range accounts.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			management.ServiceAccounts = append(management.ServiceAccounts, models.ServiceAccount{
				ID:      firstField(item, words.Management.Account.ID),
				Role:    firstField(item, words.Management.Account.Role),
				KeyHash: firstField(item, words.Management.Account.KeyHash),
			})
		}
	}
	return management
}

func firstField(node *yaml.Node, name string) string {
	value, _ := fieldValue(node, name)
	return value
}

func collectObservability(current models.Observability, section string, node *yaml.Node, words runtimeWords) models.Observability {
	switch section {
	case words.Section.Logging:
		current.Logging.Format, _ = fieldValue(node, words.Logging.Format)
		if access := mappingNode(node, words.Logging.Access); access != nil && access.Kind == yaml.SequenceNode {
			for _, item := range access.Content {
				if item.Kind == yaml.ScalarNode && item.Value != "" {
					current.Logging.Access = append(current.Logging.Access, item.Value)
				}
			}
		}
	case words.Section.Metrics:
		if prometheus, ok := fieldValue(node, words.Metrics.Prometheus); ok {
			if parsed, err := strconv.ParseBool(prometheus); err == nil {
				current.Metrics.Prometheus = parsed
			}
		}
		if otlp := mappingNode(node, words.Metrics.OTLP); otlp != nil {
			current.Metrics.OTLP = &models.MetricsOTLP{}
			current.Metrics.OTLP.Endpoint, _ = fieldValue(otlp, words.Metrics.Endpoint)
			current.Metrics.OTLP.Interval, _ = fieldValue(otlp, words.Metrics.Interval)
		}
	case words.Section.Tracing:
		if otlp := mappingNode(node, words.Tracing.OTLP); otlp != nil {
			current.Tracing.OTLP = &models.TracingOTLP{}
			current.Tracing.OTLP.Endpoint, _ = fieldValue(otlp, words.Tracing.Endpoint)
		}
		current.Tracing.Sampling, _ = fieldValue(node, words.Tracing.Sampling)
	}
	return current
}

func readManifest(path string) *yaml.Node {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return nil
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return document.Content[0]
}

func mappingNode(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == name {
			return node.Content[index+1]
		}
	}
	return nil
}

func fieldValue(node *yaml.Node, name string) (string, bool) {
	target := mappingNode(node, name)
	if target == nil || target.Kind != yaml.ScalarNode {
		return "", false
	}
	return target.Value, true
}
