package config

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

func newHasher() hash.Hash { return sha256.New() }

func CompileGateway(path string) (models.CompiledGraph, error) {
	compiled, loaded, err := supervise(path)
	if err != nil {
		return models.CompiledGraph{}, err
	}
	return buildCompiled(path, loaded, compiled)
}

func buildCompiled(path string, loaded contractFile, compiled *graph) (models.CompiledGraph, error) {
	graph := models.CompiledGraph{
		Revision: models.Revision{
			Value:  path,
			Digest: hex.EncodeToString(compiled.hasher.Sum(nil)),
		},
		Sites: map[string]models.Site{},
	}
	base := filepath.Dir(path)
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
				collectSites(node, loaded.Runtime, base, graph.Sites)
			}
		}
	}
	return graph, nil
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
		}
		routes = append(routes, route)
	}
	return routes, nil
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

func collectSites(node *yaml.Node, words runtimeWords, base string, sites map[string]models.Site) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, body := node.Content[index], node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		sites[name.Value] = compileSite(body, words, base)
	}
}

func compileSite(node *yaml.Node, words runtimeWords, base string) models.Site {
	site := models.Site{Index: words.Site.IndexDefault}
	source := mappingNode(node, words.Site.Source)
	if source == nil {
		return site
	}
	if kind, ok := fieldValue(source, words.Site.Type); ok && kind == words.Directory {
		site.Source = models.SourceDirectory
	}
	if root, ok := fieldValue(source, words.Site.Root); ok && root != "" {
		site.Root = root
		if !filepath.IsAbs(root) {
			site.Root = filepath.Join(base, root)
		}
	}
	if site.Source != models.SourceDirectory {
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