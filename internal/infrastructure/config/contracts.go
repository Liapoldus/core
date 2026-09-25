// Package config contains configuration compiler infrastructure adapters.
package config

import (
	"encoding/json"
	"sync"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type ServiceKeyWords struct {
	RolePlatformAdmin string `yaml:"rolePlatformAdmin"`
	KeyBytes          int    `yaml:"keyBytes"`
	HashCost          int    `yaml:"hashCost"`
}

type CLIWords struct {
	Commands struct {
		Serve  string `yaml:"serve"`
		Access string `yaml:"access"`
	} `yaml:"commands"`
	Access struct {
		Bootstrap string `yaml:"bootstrap"`
	} `yaml:"access"`
	Flags struct {
		Output string `yaml:"output"`
		Config string `yaml:"config"`
	} `yaml:"flags"`
	ServiceKey ServiceKeyWords `yaml:"serviceKey"`
	Outputs    struct {
		Text string `yaml:"text"`
		JSON string `yaml:"json"`
	} `yaml:"outputs"`
	Environment struct {
		GatewayConfig string `yaml:"gatewayConfig"`
	} `yaml:"environment"`
	Paths struct {
		DefaultConfig string `yaml:"defaultConfig"`
	} `yaml:"paths"`
	Codes struct {
		AccessBootstrapConflict string `yaml:"accessBootstrapConflict"`
		ConfigNotFound          string `yaml:"configNotFound"`
		ConfigInvalid           string `yaml:"configInvalid"`
	} `yaml:"codes"`
	Exits struct {
		OK          int `yaml:"ok"`
		Internal    int `yaml:"internal"`
		Arguments   int `yaml:"arguments"`
		Validation  int `yaml:"validation"`
		Conflict    int `yaml:"conflict"`
		Unavailable int `yaml:"unavailable"`
	} `yaml:"exits"`
	Sources struct {
		Flag        string `yaml:"flag"`
		Environment string `yaml:"environment"`
		System      string `yaml:"system"`
	} `yaml:"sources"`
	JSON struct {
		OK      string `yaml:"ok"`
		Problem string `yaml:"problem"`
		Code    string `yaml:"code"`
		Detail  string `yaml:"detail"`
		Token   string `yaml:"token"`
	} `yaml:"json"`
	Text struct {
		OK string `yaml:"ok"`
	} `yaml:"text"`
	Diagnostics struct {
		CommandExpected         string `yaml:"commandExpected"`
		ConfigNotFound          string `yaml:"configNotFound"`
		ConfigInvalid           string `yaml:"configInvalid"`
		OutputInvalid           string `yaml:"outputInvalid"`
		ConfigRequired          string `yaml:"configRequired"`
		ConfigLookupFailed      string `yaml:"configLookupFailed"`
		AccessBootstrapConflict string `yaml:"accessBootstrapConflict"`
	} `yaml:"diagnostics"`
}

type Words struct {
	CLI CLIWords
}

var wordsOnce = sync.OnceValues(loadWords)

func loadWords() (Words, error) {
	cli, err := loadCLI()
	if err != nil {
		return Words{}, err
	}
	return Words{CLI: cli}, nil
}

func LoadWords() (Words, error) { return wordsOnce() }

func LoadCLI() (CLIWords, error) {
	words, err := LoadWords()
	return words.CLI, err
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
		BearerRequired      string `yaml:"bearerRequired"`
		RouteNotFound       string `yaml:"routeNotFound"`
		IdempotencyConflict string `yaml:"idempotencyConflict"`
		RegistryUnavailable string `yaml:"registryUnavailable"`
		MTLSRequired        string `yaml:"mtlsRequired"`
		PluginUnavailable   string `yaml:"pluginUnavailable"`
		GroupNotFound       string `yaml:"groupNotFound"`
		GroupAlreadyExists  string `yaml:"groupAlreadyExists"`
		InvalidRequest      string `yaml:"invalidRequest"`
	} `yaml:"codes"`
	Paths struct {
		Healthz          string `yaml:"healthz"`
		Status           string `yaml:"status"`
		Config           string `yaml:"config"`
		ConfigValidate   string `yaml:"configValidate"`
		ConfigReload     string `yaml:"configReload"`
		Reload           string `yaml:"reload"`
		Listeners        string `yaml:"listeners"`
		Upstreams        string `yaml:"upstreams"`
		Plugins          string `yaml:"plugins"`
		AdminSurfaces    string `yaml:"adminSurfaces"`
		AdminPages       string `yaml:"adminPages"`
		Restart          string `yaml:"restart"`
		Logs             string `yaml:"logs"`
		TLS              string `yaml:"tls"`
		Renew            string `yaml:"renew"`
		Revoke           string `yaml:"revoke"`
		Operations       string `yaml:"operations"`
		Audit            string `yaml:"audit"`
		Groups           string `yaml:"groups"`
		GroupByID        string `yaml:"groupByID"`
		GroupIDSeparator string `yaml:"groupIDSeparator"`
		GroupIDPattern   string `yaml:"groupIDPattern"`
	} `yaml:"paths"`
	Methods struct {
		Get    string `yaml:"get"`
		Post   string `yaml:"post"`
		Put    string `yaml:"put"`
		Delete string `yaml:"delete"`
	} `yaml:"methods"`
	JSON struct {
		RequestID          string `yaml:"requestId"`
		OperationID        string `yaml:"operationId"`
		State              string `yaml:"state"`
		Items              string `yaml:"items"`
		NextCursor         string `yaml:"nextCursor"`
		YAML               string `yaml:"yaml"`
		Revision           string `yaml:"revision"`
		Digest             string `yaml:"digest"`
		Valid              string `yaml:"valid"`
		IdempotencyKey     string `yaml:"idempotencyKey"`
		ExpectedRevision   string `yaml:"expectedRevision"`
		ID                 string `yaml:"id"`
		ReplyTo            string `yaml:"replyTo"`
		Status             string `yaml:"status"`
		Slug               string `yaml:"slug"`
		Route              string `yaml:"route"`
		Root               string `yaml:"root"`
		CurrentRevision    string `yaml:"currentRevision"`
		PreviousRevision   string `yaml:"previousRevision"`
		CreatedAt          string `yaml:"createdAt"`
		StartedAt          string `yaml:"startedAt"`
		FinishedAt         string `yaml:"finishedAt"`
		Result             string `yaml:"result"`
		Problem            string `yaml:"problem"`
		Timestamp          string `yaml:"timestamp"`
		Actor              string `yaml:"actor"`
		Action             string `yaml:"action"`
		Resource           string `yaml:"resource"`
		DigestBefore       string `yaml:"digestBefore"`
		DigestAfter        string `yaml:"digestAfter"`
		Name               string `yaml:"name"`
		Type               string `yaml:"type"`
		Address            string `yaml:"address"`
		ActiveConnections  string `yaml:"activeConnections"`
		Healthy            string `yaml:"healthy"`
		Capabilities       string `yaml:"capabilities"`
		Limits             string `yaml:"limits"`
		Health             string `yaml:"health"`
		Profile            string `yaml:"profile"`
		Domain             string `yaml:"domain"`
		Serial             string `yaml:"serial"`
		NotAfter           string `yaml:"notAfter"`
		Validity           string `yaml:"validity"`
		Diagnostics        string `yaml:"diagnostics"`
		Limit              string `yaml:"limit"`
		Cursor             string `yaml:"cursor"`
		Kind               string `yaml:"kind"`
		Active             string `yaml:"active"`
		Caddy              string `yaml:"caddy"`
		Variant            string `yaml:"variant"`
		BuildID            string `yaml:"buildId"`
		Modules            string `yaml:"modules"`
		Drift              string `yaml:"drift"`
		RuntimeDigest      string `yaml:"runtimeDigest"`
		CompositionDigest  string `yaml:"compositionDigest"`
		DataPlaneReadiness string `yaml:"dataPlaneReadiness"`
		Reason             string `yaml:"reason"`
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
		OK                    string `yaml:"ok"`
		Ready                 string `yaml:"ready"`
		Draining              string `yaml:"draining"`
		Failed                string `yaml:"failed"`
		Invalid               string `yaml:"invalid"`
		Publishing            string `yaml:"publishing"`
		Healthy               string `yaml:"healthy"`
		Degraded              string `yaml:"degraded"`
		Unavailable           string `yaml:"unavailable"`
		Starting              string `yaml:"starting"`
		Unhealthy             string `yaml:"unhealthy"`
		Stopped               string `yaml:"stopped"`
		Renewing              string `yaml:"renewing"`
		Pending               string `yaml:"pending"`
		Running               string `yaml:"running"`
		Succeeded             string `yaml:"succeeded"`
		Empty                 string `yaml:"empty"`
		NotReady              string `yaml:"notReady"`
		SystemReleaseRequired string `yaml:"systemReleaseRequired"`
		CaddyUnavailable      string `yaml:"caddyUnavailable"`
		RecoveryRequired      string `yaml:"recoveryRequired"`
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

type AuditWords struct {
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
			ConfigReload string `yaml:"configReload"`
			ConfigUpdate string `yaml:"configUpdate"`
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
		AccessDefault      []string `yaml:"accessDefault"`
		ApplicationDefault []string `yaml:"applicationDefault"`
		AccessSinks        struct {
			Stdout string `yaml:"stdout"`
			Stderr string `yaml:"stderr"`
		} `yaml:"accessSinks"`
		ApplicationSinks struct {
			Stdout string `yaml:"stdout"`
			Stderr string `yaml:"stderr"`
		} `yaml:"applicationSinks"`
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
		SamplingParentBased                 string `yaml:"samplingParentBased"`
		SamplingAlwaysOn                    string `yaml:"samplingAlwaysOn"`
		SamplingAlwaysOff                   string `yaml:"samplingAlwaysOff"`
		InitializationTimeout               string `yaml:"initializationTimeout"`
		ShutdownTimeout                     string `yaml:"shutdownTimeout"`
		InvalidSamplingMessage              string `yaml:"invalidSamplingMessage"`
		InvalidInitializationTimeoutMessage string `yaml:"invalidInitializationTimeoutMessage"`
		ScopeName                           string `yaml:"scopeName"`
		ServiceName                         string `yaml:"serviceName"`
		ServiceNameAttribute                string `yaml:"serviceNameAttribute"`
		SpanName                            string `yaml:"spanName"`
		ExporterName                        string `yaml:"exporterName"`
		ExportFailureMessage                string `yaml:"exportFailureMessage"`
		Attributes                          struct {
			Method   string `yaml:"method"`
			Listener string `yaml:"listener"`
			Status   string `yaml:"status"`
		} `yaml:"attributes"`
	} `yaml:"tracing"`
	Redaction []string `yaml:"redaction"`
}

func LoadAudit() (AuditWords, error) {
	contents, err := assets.Contract(assets.AuditFields)
	if err != nil {
		return AuditWords{}, err
	}
	var loaded AuditWords
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return AuditWords{}, err
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
