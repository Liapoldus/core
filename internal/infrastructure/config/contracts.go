// Package config contains configuration compiler infrastructure adapters.
package config

import (
	"sync"

	"github.com/Liapoldus/core/assets"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type CLIWords struct {
	Commands struct {
		Serve  string `yaml:"serve"`
		Config string `yaml:"config"`
	} `yaml:"commands"`
	Subcommands struct {
		Path     string `yaml:"path"`
		Validate string `yaml:"validate"`
		Print    string `yaml:"print"`
		Format   string `yaml:"format"`
		Explain  string `yaml:"explain"`
		Diff     string `yaml:"diff"`
	} `yaml:"subcommands"`
	Flags struct {
		Output       string `yaml:"output"`
		Config       string `yaml:"config"`
		ConfigDir    string `yaml:"configDir"`
		NoManagement string `yaml:"noManagement"`
	} `yaml:"flags"`
	Serve struct {
		GracefulTimeout string `yaml:"gracefulTimeout"`
	} `yaml:"serve"`
	Outputs struct {
		Text string `yaml:"text"`
		JSON string `yaml:"json"`
	} `yaml:"outputs"`
	Environment struct {
		GatewayConfig string `yaml:"gatewayConfig"`
		ConfigDir     string `yaml:"configDir"`
	} `yaml:"environment"`
	Paths struct {
		DefaultConfig string `yaml:"defaultConfig"`
		FileName      string `yaml:"fileName"`
	} `yaml:"paths"`
	Codes struct {
		ConfigNotFound string `yaml:"configNotFound"`
		ConfigInvalid  string `yaml:"configInvalid"`
		UnknownField   string `yaml:"unknownField"`
	} `yaml:"codes"`
	Exits struct {
		OK         int `yaml:"ok"`
		Internal   int `yaml:"internal"`
		Arguments  int `yaml:"arguments"`
		Validation int `yaml:"validation"`
	} `yaml:"exits"`
	Sources struct {
		Flag                 string `yaml:"flag"`
		Environment          string `yaml:"environment"`
		FlagDirectory        string `yaml:"flagDirectory"`
		EnvironmentDirectory string `yaml:"environmentDirectory"`
		System               string `yaml:"system"`
	} `yaml:"sources"`
	JSON struct {
		OK        string `yaml:"ok"`
		Command   string `yaml:"command"`
		Path      string `yaml:"path"`
		Source    string `yaml:"source"`
		Valid     string `yaml:"valid"`
		Document  string `yaml:"document"`
		Problem   string `yaml:"problem"`
		Code      string `yaml:"code"`
		Detail    string `yaml:"detail"`
		Report    string `yaml:"report"`
		Name      string `yaml:"name"`
		Type      string `yaml:"type"`
		Address   string `yaml:"address"`
		Index     string `yaml:"index"`
		Site      string `yaml:"site"`
		Listeners string `yaml:"listeners"`
		Routes    string `yaml:"routes"`
		Sites     string `yaml:"sites"`
		Issues    string `yaml:"issues"`
		Kind      string `yaml:"kind"`
		Listener  string `yaml:"listener"`
		Diff      string `yaml:"diff"`
		Added     string `yaml:"added"`
		Removed   string `yaml:"removed"`
		Changed   string `yaml:"changed"`
		Section   string `yaml:"section"`
	} `yaml:"json"`
	Display struct {
		Path     string `yaml:"path"`
		Validate string `yaml:"validate"`
		Print    string `yaml:"print"`
		Format   string `yaml:"format"`
		Explain  string `yaml:"explain"`
		Diff     string `yaml:"diff"`
	} `yaml:"display"`
	Explain struct {
		Listener string `yaml:"listener"`
		Route    string `yaml:"route"`
		Site     string `yaml:"site"`
		Issue    string `yaml:"issue"`
	} `yaml:"explain"`
	Diff struct {
		Added   string `yaml:"added"`
		Removed string `yaml:"removed"`
		Changed string `yaml:"changed"`
		Section string `yaml:"section"`
		Entry   string `yaml:"entry"`
	} `yaml:"diff"`
	Text struct {
		OK string `yaml:"ok"`
	} `yaml:"text"`
	Diagnostics struct {
		CommandExpected      string `yaml:"commandExpected"`
		UnknownConfigCommand string `yaml:"unknownConfigCommand"`
		ConfigNotFound       string `yaml:"configNotFound"`
		ConfigInvalid        string `yaml:"configInvalid"`
		OutputInvalid        string `yaml:"outputInvalid"`
		ConfigRequired       string `yaml:"configRequired"`
		ConfigDirRequired    string `yaml:"configDirRequired"`
		ConfigLookupFailed   string `yaml:"configLookupFailed"`
	} `yaml:"diagnostics"`
}

type Words struct {
	CLI      CLIWords
	Registry models.RegistryLayout
	Snapshot models.SnapshotLayout
}

var wordsOnce = sync.OnceValues(loadWords)

func loadWords() (Words, error) {
	cli, err := loadCLI()
	if err != nil {
		return Words{}, err
	}
	registry, err := loadRegistry()
	if err != nil {
		return Words{}, err
	}
	snapshot, err := loadSnapshot()
	if err != nil {
		return Words{}, err
	}
	return Words{CLI: cli, Registry: registry, Snapshot: snapshot}, nil
}

func LoadWords() (Words, error) { return wordsOnce() }

func LoadCLI() (CLIWords, error) {
	words, err := LoadWords()
	return words.CLI, err
}

func LoadRegistryLayout() (models.RegistryLayout, error) {
	words, err := LoadWords()
	return words.Registry, err
}

func LoadSnapshotLayout() (models.SnapshotLayout, error) {
	words, err := LoadWords()
	return words.Snapshot, err
}

type registryFile struct {
	Sites           string `yaml:"sites"`
	Releases        string `yaml:"releases"`
	Current         string `yaml:"current"`
	Previous        string `yaml:"previous"`
	StagePrefix     string `yaml:"stagePrefix"`
	Manifest        string `yaml:"manifest"`
	ManifestMissing string `yaml:"manifestMissing"`
	UnsafeSource    string `yaml:"unsafeSource"`
}

func loadRegistry() (models.RegistryLayout, error) {
	contents, err := assets.Contract(assets.RegistryFields)
	if err != nil {
		return models.RegistryLayout{}, err
	}
	var loaded registryFile
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return models.RegistryLayout{}, err
	}
	return models.RegistryLayout{
		Sites:           loaded.Sites,
		Releases:        loaded.Releases,
		Current:         loaded.Current,
		Previous:        loaded.Previous,
		StagePrefix:     loaded.StagePrefix,
		Manifest:        loaded.Manifest,
		ManifestMissing: loaded.ManifestMissing,
		UnsafeSource:    loaded.UnsafeSource,
	}, nil
}

type snapshotFile struct {
	Active         string `yaml:"active"`
	PreparedPrefix string `yaml:"preparedPrefix"`
	Drained        string `yaml:"drained"`
}

func loadSnapshot() (models.SnapshotLayout, error) {
	contents, err := assets.Contract(assets.SnapshotFields)
	if err != nil {
		return models.SnapshotLayout{}, err
	}
	var loaded snapshotFile
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return models.SnapshotLayout{}, err
	}
	return models.SnapshotLayout{
		Active:         loaded.Active,
		PreparedPrefix: loaded.PreparedPrefix,
		Drained:        loaded.Drained,
	}, nil
}

func loadCLI() (CLIWords, error) {
	contents, err := assets.Contract(assets.CLIFields)
	if err != nil {
		return CLIWords{}, err
	}
	var loaded CLIWords
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return CLIWords{}, err
	}
	return loaded, nil
}
