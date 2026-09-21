package config

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

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
		}
	}
	return nil
}

func buildCompiled(path string, loaded contractFile, compiled *graph) (models.CompiledGraph, error) {
	graph := models.CompiledGraph{
		Revision: models.Revision{
			Value:  path,
			Digest: hex.EncodeToString(compiled.hasher.Sum(nil)),
		},
		Sites:   map[string]models.Site{},
		Secrets: map[string]models.Secret{},
		Upstreams: map[string]models.Upstream{},
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
				collectSites(node, loaded.Runtime, base, registryRoot, layout, graph.Sites)
			case loaded.Upstreams:
				if err := collectUpstreams(node, loaded.Runtime, graph.Upstreams); err != nil {
					return models.CompiledGraph{}, err
				}
			}
		}
	}
	for name, value := range compiled.secrets {
		graph.Secrets[name] = models.Secret{Value: value}
	}
	return graph, nil
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
		listener.IsHTTP = typ == words.HTTP
		listener.Address, _ = fieldValue(body, words.Listener.Address)
		routes, err := collectRoutes(mappingNode(body, words.Listener.Routes), words)
		if err != nil {
			return nil, err
		}
		listener.Routes = routes
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
		}
		routes = append(routes, route)
	}
	return routes, nil
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

func collectSites(node *yaml.Node, words runtimeWords, base, registryRoot string, layout models.RegistryLayout, sites map[string]models.Site) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, body := node.Content[index], node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		sites[name.Value] = compileSite(body, words, base, registryRoot, layout)
	}
}

func compileSite(node *yaml.Node, words runtimeWords, base, registryRoot string, layout models.RegistryLayout) models.Site {
	site := models.Site{Index: words.Site.IndexDefault}
	source := mappingNode(node, words.Site.Source)
	if source == nil {
		return site
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
		return site
	}
	if index, err := manifestIndex(filepath.Join(site.Root, words.Site.ManifestFileName), words.Site.Index); err == nil && index != "" {
		site.Index = index
	}
	return site
}

func manifestIndex(path string, field string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return "", err
	}
	if len(document.Content) == 0 {
		return "", os.ErrInvalid
	}
	index, ok := fieldValue(document.Content[0], field)
	if !ok {
		return "", os.ErrInvalid
	}
	return index, nil
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
