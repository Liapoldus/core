// Package config contains configuration compiler infrastructure adapters.
package config

import (
	"encoding/json"
	"sync"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type AccountWords struct {
	Create            string `yaml:"create"`
	Rotate            string `yaml:"rotate"`
	Revoke            string `yaml:"revoke"`
	RolePlatformAdmin string `yaml:"rolePlatformAdmin"`
	KeyPrefix         string `yaml:"keyPrefix"`
	KeyBytes          int    `yaml:"keyBytes"`
	HashCost          int    `yaml:"hashCost"`
	HashExtension     string `yaml:"hashExtension"`
	SecretsDir        string `yaml:"secretsDir"`
	AccountsDir       string `yaml:"accountsDir"`
	KeyHashWord       string `yaml:"keyHashWord"`
	SaveMessage       string `yaml:"saveMessage"`
	RotateMessage     string `yaml:"rotateMessage"`
	RevokedMessage    string `yaml:"revokedMessage"`
}

type CLIWords struct {
	Commands struct {
		Serve    string `yaml:"serve"`
		Config   string `yaml:"config"`
		Accounts string `yaml:"accounts"`
		Site     string `yaml:"site"`
	} `yaml:"commands"`
	Subcommands struct {
		Path     string `yaml:"path"`
		Validate string `yaml:"validate"`
		Print    string `yaml:"print"`
		Format   string `yaml:"format"`
		Explain  string `yaml:"explain"`
		Diff     string `yaml:"diff"`
	} `yaml:"subcommands"`
	Site struct {
		Publish  string `yaml:"publish"`
		Rollback string `yaml:"rollback"`
		Current  string `yaml:"current"`
		Previous string `yaml:"previous"`
	} `yaml:"site"`
	Flags struct {
		Output       string `yaml:"output"`
		Config       string `yaml:"config"`
		ConfigDir    string `yaml:"configDir"`
		NoManagement string `yaml:"noManagement"`
		Role         string `yaml:"role"`
	} `yaml:"flags"`
	Accounts AccountWords `yaml:"accounts"`
	Serve    struct {
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
		ConfigNotFound         string `yaml:"configNotFound"`
		ConfigInvalid          string `yaml:"configInvalid"`
		UnknownField           string `yaml:"unknownField"`
		NoPreviousRelease      string `yaml:"noPreviousRelease"`
		WAFProviderUnavailable string `yaml:"wafProviderUnavailable"`
		BodyTooLarge           string `yaml:"bodyTooLarge"`
		ResourceExhausted      string `yaml:"resourceExhausted"`
		PluginTimeout          string `yaml:"pluginTimeout"`
		RateLimited            string `yaml:"rateLimited"`
		SiteInvalid            string `yaml:"siteInvalid"`
		SiteSourceImmutable    string `yaml:"siteSourceImmutable"`
		RegistryUnavailable    string `yaml:"registryUnavailable"`
		ReleaseInvalid         string `yaml:"releaseInvalid"`
	} `yaml:"codes"`
	Exits struct {
		OK            int `yaml:"ok"`
		Internal      int `yaml:"internal"`
		Arguments     int `yaml:"arguments"`
		Validation    int `yaml:"validation"`
		Conflict      int `yaml:"conflict"`
		NotFound      int `yaml:"notFound"`
		Authorization int `yaml:"authorization"`
		Unavailable   int `yaml:"unavailable"`
	} `yaml:"exits"`
	Sources struct {
		Flag          string `yaml:"flag"`
		Environment   string `yaml:"environment"`
		FlagDirectory string `yaml:"flagDirectory"`
		System        string `yaml:"system"`
	} `yaml:"sources"`
	JSON struct {
		OK               string `yaml:"ok"`
		Command          string `yaml:"command"`
		Path             string `yaml:"path"`
		Source           string `yaml:"source"`
		Valid            string `yaml:"valid"`
		Document         string `yaml:"document"`
		Problem          string `yaml:"problem"`
		Code             string `yaml:"code"`
		Detail           string `yaml:"detail"`
		Report           string `yaml:"report"`
		Name             string `yaml:"name"`
		Type             string `yaml:"type"`
		Address          string `yaml:"address"`
		Index            string `yaml:"index"`
		Site             string `yaml:"site"`
		Listeners        string `yaml:"listeners"`
		Routes           string `yaml:"routes"`
		Sites            string `yaml:"sites"`
		Issues           string `yaml:"issues"`
		Kind             string `yaml:"kind"`
		Listener         string `yaml:"listener"`
		Revision         string `yaml:"revision"`
		PreviousRevision string `yaml:"previousRevision"`
		RequestID        string `yaml:"requestId"`
		Diff             string `yaml:"diff"`
		Added            string `yaml:"added"`
		Removed          string `yaml:"removed"`
		Changed          string `yaml:"changed"`
		Section          string `yaml:"section"`
	} `yaml:"json"`
	Display struct {
		Path           string `yaml:"path"`
		Validate       string `yaml:"validate"`
		Print          string `yaml:"print"`
		Format         string `yaml:"format"`
		Explain        string `yaml:"explain"`
		Diff           string `yaml:"diff"`
		AccountsCreate string `yaml:"accountsCreate"`
		AccountsRotate string `yaml:"accountsRotate"`
		AccountsRevoke string `yaml:"accountsRevoke"`
		SitePublish    string `yaml:"sitePublish"`
		SiteRollback   string `yaml:"siteRollback"`
		SiteCurrent    string `yaml:"siteCurrent"`
		SitePrevious   string `yaml:"sitePrevious"`
	} `yaml:"display"`
	Identifiers struct {
		RequestPrefix string `yaml:"requestPrefix"`
		RequestBytes  int    `yaml:"requestBytes"`
	} `yaml:"identifiers"`
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
		OK   string `yaml:"ok"`
		Null string `yaml:"null"`
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
		RoleRequired         string `yaml:"roleRequired"`
		RoleInvalid          string `yaml:"roleInvalid"`
		AccountIDRequired    string `yaml:"accountIDRequired"`
		AccountNotFound      string `yaml:"accountNotFound"`
		HashInvalid          string `yaml:"hashInvalid"`
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

func LoadAccounts() (AccountWords, error) {
	words, err := LoadCLI()
	if err != nil {
		return AccountWords{}, err
	}
	return words.Accounts, nil
}

type registryFile struct {
	DefaultRoot     string `yaml:"defaultRoot"`
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
		DefaultRoot:     loaded.DefaultRoot,
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

type ManagementWords struct {
	Codes struct {
		RouteNotFound           string `yaml:"routeNotFound"`
		SiteSourceImmutable     string `yaml:"siteSourceImmutable"`
		IdempotencyConflict     string `yaml:"idempotencyConflict"`
		ReleaseInvalid          string `yaml:"releaseInvalid"`
		RegistryUnavailable     string `yaml:"registryUnavailable"`
		NoPreviousRelease       string `yaml:"noPreviousRelease"`
		ReleaseRevisionConflict string `yaml:"releaseRevisionConflict"`
	} `yaml:"codes"`
	Paths struct {
		Healthz        string `yaml:"healthz"`
		Status         string `yaml:"status"`
		Config         string `yaml:"config"`
		ConfigValidate string `yaml:"configValidate"`
		ConfigReload   string `yaml:"configReload"`
		Reload         string `yaml:"reload"`
		Sites          string `yaml:"sites"`
		Publish        string `yaml:"publish"`
		Rollback       string `yaml:"rollback"`
		Listeners      string `yaml:"listeners"`
		Upstreams      string `yaml:"upstreams"`
		Plugins        string `yaml:"plugins"`
		AdminSurfaces  string `yaml:"adminSurfaces"`
		AdminPages     string `yaml:"adminPages"`
		Restart        string `yaml:"restart"`
		Logs           string `yaml:"logs"`
		TLS            string `yaml:"tls"`
		Renew          string `yaml:"renew"`
		Revoke         string `yaml:"revoke"`
		Operations     string `yaml:"operations"`
		Audit          string `yaml:"audit"`
		Metrics        string `yaml:"metrics"`
	} `yaml:"paths"`
	Methods struct {
		Get    string `yaml:"get"`
		Post   string `yaml:"post"`
		Put    string `yaml:"put"`
		Delete string `yaml:"delete"`
	} `yaml:"methods"`
	JSON struct {
		RequestID               string `yaml:"requestId"`
		OperationID             string `yaml:"operationId"`
		State                   string `yaml:"state"`
		Items                   string `yaml:"items"`
		NextCursor              string `yaml:"nextCursor"`
		YAML                    string `yaml:"yaml"`
		Revision                string `yaml:"revision"`
		Digest                  string `yaml:"digest"`
		Valid                   string `yaml:"valid"`
		Source                  string `yaml:"source"`
		IdempotencyKey          string `yaml:"idempotencyKey"`
		ExpectedCurrentRevision string `yaml:"expectedCurrentRevision"`
		ExpectedRevision        string `yaml:"expectedRevision"`
		ID                      string `yaml:"id"`
		ReplyTo                 string `yaml:"replyTo"`
		Status                  string `yaml:"status"`
		Slug                    string `yaml:"slug"`
		Route                   string `yaml:"route"`
		Root                    string `yaml:"root"`
		CurrentRevision         string `yaml:"currentRevision"`
		PreviousRevision        string `yaml:"previousRevision"`
		CreatedAt               string `yaml:"createdAt"`
		StartedAt               string `yaml:"startedAt"`
		FinishedAt              string `yaml:"finishedAt"`
		Result                  string `yaml:"result"`
		Problem                 string `yaml:"problem"`
		Timestamp               string `yaml:"timestamp"`
		Actor                   string `yaml:"actor"`
		Action                  string `yaml:"action"`
		Resource                string `yaml:"resource"`
		DigestBefore            string `yaml:"digestBefore"`
		DigestAfter             string `yaml:"digestAfter"`
		Name                    string `yaml:"name"`
		Type                    string `yaml:"type"`
		Address                 string `yaml:"address"`
		ActiveConnections       string `yaml:"activeConnections"`
		Healthy                 string `yaml:"healthy"`
		Capabilities            string `yaml:"capabilities"`
		Limits                  string `yaml:"limits"`
		Health                  string `yaml:"health"`
		Profile                 string `yaml:"profile"`
		Domain                  string `yaml:"domain"`
		Serial                  string `yaml:"serial"`
		NotAfter                string `yaml:"notAfter"`
		Validity                string `yaml:"validity"`
		Diagnostics             string `yaml:"diagnostics"`
		Limit                   string `yaml:"limit"`
		Cursor                  string `yaml:"cursor"`
	} `yaml:"json"`
	Headers struct {
		IfMatch    string `yaml:"ifMatch"`
		RequestID  string `yaml:"requestId"`
		Location   string `yaml:"location"`
		RetryAfter string `yaml:"retryAfter"`
	} `yaml:"headers"`
	ContentTypes struct {
		YAML    string `yaml:"yaml"`
		JSON    string `yaml:"json"`
		Problem string `yaml:"problem"`
		Text    string `yaml:"text"`
	} `yaml:"contentTypes"`
	Statuses struct {
		OK          string `yaml:"ok"`
		Ready       string `yaml:"ready"`
		Draining    string `yaml:"draining"`
		Failed      string `yaml:"failed"`
		Invalid     string `yaml:"invalid"`
		Publishing  string `yaml:"publishing"`
		Healthy     string `yaml:"healthy"`
		Degraded    string `yaml:"degraded"`
		Unavailable string `yaml:"unavailable"`
		Starting    string `yaml:"starting"`
		Unhealthy   string `yaml:"unhealthy"`
		Stopped     string `yaml:"stopped"`
		Renewing    string `yaml:"renewing"`
		Pending     string `yaml:"pending"`
		Running     string `yaml:"running"`
		Succeeded   string `yaml:"succeeded"`
	} `yaml:"statuses"`
	Idempotency struct {
		LimitDefault int    `yaml:"limitDefault"`
		LimitMax     int    `yaml:"limitMax"`
		LimitMin     int    `yaml:"limitMin"`
		KeyChars     int    `yaml:"keyChars"`
		KeyMin       int    `yaml:"keyMin"`
		Window       string `yaml:"window"`
		Key          string `yaml:"key"`
		ReplyTo      string `yaml:"replyTo"`
	} `yaml:"idempotency"`
	Pagination struct {
		LimitDefault int `yaml:"limitDefault"`
		LimitMax     int `yaml:"limitMax"`
		LimitMin     int `yaml:"limitMin"`
	} `yaml:"pagination"`
	OperationState struct {
		Done     string `yaml:"done"`
		Accepted string `yaml:"accepted"`
	} `yaml:"operationState"`
}

func LoadManagement() (ManagementWords, error) {
	contents, err := assets.Contract(assets.ManagementFields)
	if err != nil {
		return ManagementWords{}, err
	}
	var loaded ManagementWords
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return ManagementWords{}, err
	}
	return loaded, nil
}

type ObservabilityWords struct {
	Audit struct {
		Directory     string `yaml:"directory"`
		Extension     string `yaml:"extension"`
		RetentionDays int    `yaml:"retentionDays"`
		DateLayout    string `yaml:"dateLayout"`
		Actors        struct {
			Anonymous   string `yaml:"anonymous"`
			StaticToken string `yaml:"staticToken"`
		} `yaml:"actors"`
		Actions struct {
			ConfigReload  string `yaml:"configReload"`
			ConfigUpdate  string `yaml:"configUpdate"`
			SitePublished string `yaml:"sitePublished"`
		} `yaml:"actions"`
		Resources struct {
			Gateway string `yaml:"gateway"`
		} `yaml:"resources"`
		Results struct {
			Succeeded string `yaml:"succeeded"`
			Failed    string `yaml:"failed"`
		} `yaml:"results"`
		StorageUnavailable struct {
			Code   string `yaml:"code"`
			Detail string `yaml:"detail"`
		} `yaml:"storageUnavailable"`
	} `yaml:"audit"`
	Operations struct {
		Retention string `yaml:"retention"`
		Directory string `yaml:"directory"`
	} `yaml:"operations"`
	Metrics struct {
		IntervalDefault        string `yaml:"intervalDefault"`
		ScopeName              string `yaml:"scopeName"`
		Unit                   string `yaml:"unit"`
		ExporterName           string `yaml:"exporterName"`
		ExportFailureMessage   string `yaml:"exportFailureMessage"`
		InvalidIntervalMessage string `yaml:"invalidIntervalMessage"`
		Help                   struct {
			RequestTotal        string `yaml:"requestTotal"`
			RequestDuration     string `yaml:"requestDuration"`
			ManagementTotal     string `yaml:"managementTotal"`
			AuditRecordsTotal   string `yaml:"auditRecordsTotal"`
			ExportFailuresTotal string `yaml:"exportFailuresTotal"`
		} `yaml:"help"`
		Names struct {
			RequestTotal        string `yaml:"requestTotal"`
			RequestDuration     string `yaml:"requestDuration"`
			ManagementTotal     string `yaml:"managementTotal"`
			AuditRecordsTotal   string `yaml:"auditRecordsTotal"`
			ExportFailuresTotal string `yaml:"exportFailuresTotal"`
		} `yaml:"names"`
		Labels struct {
			Listener string `yaml:"listener"`
			Route    string `yaml:"route"`
			Site     string `yaml:"site"`
			Method   string `yaml:"method"`
			Status   string `yaml:"status"`
			Exporter string `yaml:"exporter"`
			Action   string `yaml:"action"`
			Result   string `yaml:"result"`
		} `yaml:"labels"`
	} `yaml:"metrics"`
	Logging struct {
		Formats struct {
			JSON string `yaml:"json"`
		} `yaml:"formats"`
		AccessDefault []string `yaml:"accessDefault"`
		AccessSinks   struct {
			Stdout string `yaml:"stdout"`
			Stderr string `yaml:"stderr"`
		} `yaml:"accessSinks"`
		AccessFields struct {
			RequestIDHeader string `yaml:"requestIDHeader"`
			Timestamp       string `yaml:"timestamp"`
			RequestID       string `yaml:"requestID"`
			Listener        string `yaml:"listener"`
			Route           string `yaml:"route"`
			Method          string `yaml:"method"`
			Host            string `yaml:"host"`
			Path            string `yaml:"path"`
			Status          string `yaml:"status"`
			Duration        string `yaml:"duration"`
			Bytes           string `yaml:"bytes"`
		} `yaml:"accessFields"`
	} `yaml:"logging"`
	Tracing struct {
		SamplingParentBased string `yaml:"samplingParentBased"`
	} `yaml:"tracing"`
	Redaction []string `yaml:"redaction"`
}

func LoadObservability() (ObservabilityWords, error) {
	contents, err := assets.Contract(assets.ObservabilityFields)
	if err != nil {
		return ObservabilityWords{}, err
	}
	var loaded ObservabilityWords
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return ObservabilityWords{}, err
	}
	return loaded, nil
}

type errorCatalogFile struct {
	Titles map[string]string `json:"titles"`
	Errors []struct {
		Code    string `json:"code"`
		Status  int    `json:"status"`
		Type    string `json:"type"`
		Detail  string `json:"detail"`
		CLIExit int    `json:"cliExit"`
	} `json:"errors"`
}

type ErrorCatalog struct {
	codes map[string]models.Problem
}

func (catalog ErrorCatalog) Lookup(code string) (models.Problem, bool) {
	problem, exists := catalog.codes[code]
	return problem, exists
}

func LoadErrorCatalog() (ErrorCatalog, error) {
	contents, err := assets.Contract(assets.ErrorsJSON)
	if err != nil {
		return ErrorCatalog{}, err
	}
	var loaded errorCatalogFile
	if err := json.Unmarshal(contents, &loaded); err != nil {
		return ErrorCatalog{}, err
	}
	catalog := ErrorCatalog{codes: make(map[string]models.Problem, len(loaded.Errors))}
	for _, entry := range loaded.Errors {
		catalog.codes[entry.Code] = models.Problem{
			Type:    entry.Type,
			Title:   loaded.Titles[entry.Code],
			Status:  entry.Status,
			Code:    entry.Code,
			Detail:  entry.Detail,
			CLIExit: entry.CLIExit,
		}
	}
	return catalog, nil
}

func (catalog ErrorCatalog) Problem(code, detail, path string) models.Problem {
	problem, ok := catalog.codes[code]
	if !ok {
		return models.Problem{
			Type:   "https://liapoldus.dev/problems/internal_error",
			Title:  "Internal error",
			Status: 500,
			Code:   "internal_error",
			Detail: detail,
			Path:   path,
		}
	}
	problem.Detail = detail
	problem.Path = path
	return problem
}
