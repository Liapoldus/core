package config

import (
	"encoding/json"
	"sort"

	"gopkg.in/yaml.v3"
)

type DiffReport struct {
	Added   []DiffChange
	Removed []DiffChange
	Changed []DiffChange
}

type DiffChange struct {
	Section string
	Name    string
}

// Diff compares the effective documents of two configurations. The effective
// document is the merged, resolved, and redacted view used by config print, so
// differences are reported on what the gateway would actually apply: variable
// substitutions are already resolved and secrets compare only as references,
// never as values. Entry-level differences are reported for the listeners and
// sites sections; every other section is compared as a whole.
func Diff(first, second string) (DiffReport, error) {
	if first == second {
		return DiffReport{}, nil
	}
	loaded, err := loadContractFile()
	if err != nil {
		return DiffReport{}, err
	}
	left, err := effectiveDocument(first)
	if err != nil {
		return DiffReport{}, err
	}
	right, err := effectiveDocument(second)
	if err != nil {
		return DiffReport{}, err
	}
	return compareDocuments(left, right, loaded), nil
}

func compareDocuments(left, right *yaml.Node, loaded contractFile) DiffReport {
	report := DiffReport{}
	first := decodeMapping(left)
	second := decodeMapping(right)
	for section := range second {
		if _, exists := first[section]; !exists {
			report.Added = append(report.Added, DiffChange{Section: section})
		}
	}
	for section := range first {
		if _, exists := second[section]; !exists {
			report.Removed = append(report.Removed, DiffChange{Section: section})
		}
	}
	for _, section := range unionKeys(first, second) {
		firstValue, presentFirst := first[section]
		secondValue, presentSecond := second[section]
		if !presentFirst || !presentSecond {
			continue
		}
		if section == loaded.Listeners || section == loaded.Sites {
			if isEntryMap(firstValue) && isEntryMap(secondValue) {
				compareEntries(&report, section, first[section].(map[string]any), second[section].(map[string]any))
				continue
			}
		}
		if canonical(firstValue) != canonical(secondValue) {
			report.Changed = append(report.Changed, DiffChange{Section: section})
		}
	}
	sortChanges(report.Added)
	sortChanges(report.Removed)
	sortChanges(report.Changed)
	return report
}

func compareEntries(report *DiffReport, section string, first, second map[string]any) {
	for name := range second {
		if _, exists := first[name]; !exists {
			report.Added = append(report.Added, DiffChange{Section: section, Name: name})
		}
	}
	for name := range first {
		if _, exists := second[name]; !exists {
			report.Removed = append(report.Removed, DiffChange{Section: section, Name: name})
		}
	}
	for name, firstValue := range first {
		secondValue, exists := second[name]
		if exists && canonical(firstValue) != canonical(secondValue) {
			report.Changed = append(report.Changed, DiffChange{Section: section, Name: name})
		}
	}
}

func decodeMapping(node *yaml.Node) map[string]any {
	value := map[string]any{}
	if node == nil || node.Kind != yaml.MappingNode {
		return value
	}
	if err := node.Decode(&value); err != nil {
		return map[string]any{}
	}
	return value
}

func isEntryMap(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

func canonical(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func unionKeys(first, second map[string]any) []string {
	seen := map[string]bool{}
	for section := range first {
		seen[section] = true
	}
	for section := range second {
		seen[section] = true
	}
	keys := make([]string, 0, len(seen))
	for section := range seen {
		keys = append(keys, section)
	}
	sort.Strings(keys)
	return keys
}

func sortChanges(changes []DiffChange) {
	sort.SliceStable(changes, func(a, b int) bool {
		if changes[a].Section == changes[b].Section {
			return changes[a].Name < changes[b].Name
		}
		return changes[a].Section < changes[b].Section
	})
}
