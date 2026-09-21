package config

import (
	"sort"

	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type ExplainReport struct {
	Listeners []ExplainListener
	Routes    []ExplainRoute
	Sites     []ExplainSite
	Issues    []ExplainIssue
}

type ExplainListener struct {
	Name    string
	Type    string
	Address string
}

type ExplainRoute struct {
	Listener string
	Index    int
	Site     string
}

type ExplainSite struct {
	Name  string
	Index string
}

type ExplainIssue struct {
	Kind string
	Name string
}

// Explain describes what the gateway would do with a configuration: which
// listeners serve which routes to which sites, and which definitions require
// attention (sites redefined across documents, or defined but never targeted).
func Explain(path string) (ExplainReport, error) {
	compiled, loaded, err := supervise(path)
	if err != nil {
		return ExplainReport{}, err
	}
	graph, err := buildCompiled(path, loaded, compiled)
	if err != nil {
		return ExplainReport{}, err
	}
	if err := validateReferences(graph); err != nil {
		return ExplainReport{}, err
	}
	return buildExplain(compiled, loaded, graph), nil
}

func buildExplain(compiled *graph, loaded contractFile, graph models.CompiledGraph) ExplainReport {
	var report ExplainReport
	referenced := map[string]bool{}
	counts := map[string]int{}
	for _, document := range compiled.documents {
		for index := 0; index < len(document.Content); index += 2 {
			key, value := document.Content[index], document.Content[index+1]
			switch key.Value {
			case loaded.Listeners:
				collectExplainListeners(value, loaded.Runtime, &report, referenced)
			case loaded.Sites:
				countSites(value, counts)
			}
		}
	}
	names := make([]string, 0, len(counts))
	for name, count := range counts {
		if count > 1 {
			report.Issues = append(report.Issues, ExplainIssue{Kind: loaded.Semantics.OverriddenSite, Name: name})
		}
		names = append(names, name)
	}
	unused := make([]string, 0, len(graph.Sites))
	for name := range graph.Sites {
		if !referenced[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	for _, name := range unused {
		report.Issues = append(report.Issues, ExplainIssue{Kind: loaded.Semantics.UnusedSite, Name: name})
	}
	sorted := make([]string, 0, len(graph.Sites))
	for name := range graph.Sites {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		report.Sites = append(report.Sites, ExplainSite{Name: name, Index: string(graph.Sites[name].Index)})
	}
	sort.SliceStable(report.Issues, func(a, b int) bool {
		if report.Issues[a].Kind == report.Issues[b].Kind {
			return report.Issues[a].Name < report.Issues[b].Name
		}
		return report.Issues[a].Kind < report.Issues[b].Kind
	})
	return report
}

func collectExplainListeners(node *yaml.Node, words runtimeWords, report *ExplainReport, referenced map[string]bool) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, body := node.Content[index], node.Content[index+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		typ, _ := fieldValue(body, words.Listener.Type)
		address, _ := fieldValue(body, words.Listener.Address)
		report.Listeners = append(report.Listeners, ExplainListener{Name: name.Value, Type: typ, Address: address})
		collectExplainRoutes(mappingNode(body, words.Listener.Routes), words, name.Value, report, referenced)
	}
}

func collectExplainRoutes(node *yaml.Node, words runtimeWords, listener string, report *ExplainReport, referenced map[string]bool) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return
	}
	for routeIndex, routeNode := range node.Content {
		if routeNode.Kind != yaml.MappingNode {
			continue
		}
		site, _ := fieldValue(mappingNode(routeNode, words.Route.Then), words.Route.Site)
		report.Routes = append(report.Routes, ExplainRoute{Listener: listener, Index: routeIndex + 1, Site: site})
		if site != "" {
			referenced[site] = true
		}
	}
}

func countSites(node *yaml.Node, counts map[string]int) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index < len(node.Content); index += 2 {
		counts[node.Content[index].Value]++
	}
}
