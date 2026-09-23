package config

import (
	"encoding/json"
	"errors"
	"hash"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	assets "github.com/Liapoldus/core"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

type contractSecretReference struct {
	EnvPrefix  string `yaml:"envPrefix"`
	FilePrefix string `yaml:"filePrefix"`
}

type validationError uint8

func (value validationError) Error() string { return string(rune(value)) }

const (
	ErrUnknownField validationError = iota + 1
	ErrInvalidDocument
	ErrUndefinedSite
	ErrUndefinedUpstream
	ErrUndefinedAuthPolicy
	ErrUndefinedWAFPolicy
	ErrUndefinedRateLimit
	ErrInvalidTarget
	ErrMultipleTerminalActions
)

type contractFile struct {
	Root          []string `yaml:"root"`
	Listener      []string `yaml:"listener"`
	Includes      string   `yaml:"includes"`
	Listeners     string   `yaml:"listeners"`
	Sites         string   `yaml:"sites"`
	Secrets       string   `yaml:"secrets"`
	Variables     string   `yaml:"variables"`
	Upstreams     string   `yaml:"upstreams"`
	RateLimits    string   `yaml:"rateLimits"`
	WAFPolicies   string   `yaml:"wafPolicies"`
	AuthPolicies  string   `yaml:"authPolicies"`
	DataProviders string   `yaml:"dataProviders"`
	Plugins       string   `yaml:"plugins"`
	Registry      struct {
		Section string `yaml:"section"`
		Path    string `yaml:"path"`
	} `yaml:"registry"`
	SecretReference contractSecretReference `yaml:"secretReference"`
	Substitution    struct {
		Open  string `yaml:"open"`
		Close string `yaml:"close"`
	} `yaml:"substitution"`
	Semantics struct {
		UnusedSite                       string `yaml:"unusedSite"`
		OverriddenSite                   string `yaml:"overriddenSite"`
		DefaultLocaleMustBeListed        string `yaml:"defaultLocaleMustBeListed"`
		ManagementNonLoopbackTLS         string `yaml:"managementNonLoopbackTLS"`
		ManagementNonLoopbackMTLS        string `yaml:"managementNonLoopbackMTLS"`
		ManagementStaticTokenNonLoopback string `yaml:"managementStaticTokenNonLoopback"`
		ManagementStaticTokenExclusive   string `yaml:"managementStaticTokenExclusive"`
		ManagementRemoteRequiresAccount  string `yaml:"managementRemoteRequiresAccount"`
	} `yaml:"semantics"`
	Codes struct {
		ConfigInvalid          string `yaml:"configInvalid"`
		ManagementTLSRequired  string `yaml:"managementTLSRequired"`
		ManagementMTLSRequired string `yaml:"managementMTLSRequired"`
		SiteInvalid            string `yaml:"siteInvalid"`
	} `yaml:"codes"`
	Runtime runtimeWords `yaml:"runtime"`
}

type runtimeWords struct {
	HTTP      string `yaml:"http"`
	Directory string `yaml:"directory"`
	Release   string `yaml:"release"`
	Section   struct {
		TLSProfiles string `yaml:"tlsProfiles"`
		Management  string `yaml:"management"`
		Logging     string `yaml:"logging"`
		Metrics     string `yaml:"metrics"`
		Tracing     string `yaml:"tracing"`
	} `yaml:"section"`
	Listener struct {
		Type               string
		Address            string
		TLSProfile         string `yaml:"tls"`
		Routes             string
		Rules              string
		Limits             string            `yaml:"limits"`
		BodyBytes          string            `yaml:"bodyBytes"`
		BodyBytesDefault   string            `yaml:"bodyBytesDefault"`
		HeaderBytes        string            `yaml:"headerBytes"`
		HeaderBytesDefault string            `yaml:"headerBytesDefault"`
		SizeUnits          map[string]uint64 `yaml:"sizeUnits"`
	}
	Site struct {
		Source           string `yaml:"source"`
		Type             string `yaml:"type"`
		Root             string `yaml:"root"`
		Index            string `yaml:"index"`
		IndexDefault     string `yaml:"indexDefault"`
		ManifestFileName string `yaml:"manifestFileName"`
		Slug             string `yaml:"slug"`
		SPA              string `yaml:"spa"`
		Locales          string `yaml:"locales"`
		DefaultLocale    string `yaml:"defaultLocale"`
		LocaleSeparator  string `yaml:"localeSeparator"`
		Redirects        string `yaml:"redirects"`
		Headers          string `yaml:"headers"`
		Cache            string `yaml:"cache"`
	}
	SiteRedirect struct {
		From     string `yaml:"from"`
		To       string `yaml:"to"`
		Status   string `yaml:"status"`
		Location string `yaml:"location"`
	} `yaml:"siteRedirect"`
	SiteCache struct {
		Static     string `yaml:"static"`
		Visibility string `yaml:"visibility"`
		MaxAge     string `yaml:"maxAge"`
	} `yaml:"siteCache"`
	TLSProfile struct {
		Certificates string `yaml:"certificates"`
		Cert         string `yaml:"cert"`
		Key          string `yaml:"key"`
		Domains      string `yaml:"domains"`
		Issuer       string `yaml:"issuer"`
		Protocols    string `yaml:"protocols"`
		ClientAuth   string `yaml:"clientAuth"`
	} `yaml:"tlsProfile"`
	ClientAuth struct {
		Mode     string `yaml:"mode"`
		CA       string `yaml:"ca"`
		Require  string `yaml:"require"`
		Optional string `yaml:"optional"`
	} `yaml:"clientAuth"`
	Logging struct {
		Format string `yaml:"format"`
		Access string `yaml:"access"`
	} `yaml:"logging"`
	Metrics struct {
		Prometheus string `yaml:"prometheus"`
		OTLP       string `yaml:"otlp"`
		Endpoint   string `yaml:"endpoint"`
		Interval   string `yaml:"interval"`
	} `yaml:"metrics"`
	Tracing struct {
		OTLP     string `yaml:"otlp"`
		Endpoint string `yaml:"endpoint"`
		Sampling string `yaml:"sampling"`
	} `yaml:"tracing"`
	Management struct {
		Listener        string `yaml:"listener"`
		Address         string `yaml:"address"`
		TLSProfile      string `yaml:"tlsProfile"`
		StaticToken     string `yaml:"staticToken"`
		ServiceAccounts string `yaml:"serviceAccounts"`
		Account         struct {
			ID                string `yaml:"id"`
			Role              string `yaml:"role"`
			KeyHash           string `yaml:"keyHash"`
			RolePlatformAdmin string `yaml:"rolePlatformAdmin"`
		} `yaml:"account"`
	} `yaml:"management"`
	Route struct {
		When      string
		Then      string
		Host      string
		Method    string
		Path      string
		Prefix    string
		Exact     string
		Regex     string
		Site      string
		Proxy     string
		Redirect  string `yaml:"redirect"`
		Rewrite   string `yaml:"rewrite"`
		Headers   string `yaml:"headers"`
		Query     string `yaml:"query"`
		Deny      string `yaml:"deny"`
		Plugin    string `yaml:"plugin"`
		Auth      string `yaml:"auth"`
		WAF       string `yaml:"waf"`
		RateLimit string `yaml:"rateLimit"`
		CORS      string `yaml:"cors"`
		Cache     string `yaml:"cache"`
	}
	RateLimit struct {
		Key      string `yaml:"key"`
		Requests string `yaml:"requests"`
		Per      string `yaml:"per"`
		Burst    string `yaml:"burst"`
	} `yaml:"rateLimit"`
	WAF struct {
		Rules       string `yaml:"rules"`
		When        string `yaml:"when"`
		Then        string `yaml:"then"`
		Path        string `yaml:"path"`
		Method      string `yaml:"method"`
		SourceIP    string `yaml:"sourceIP"`
		Headers     string `yaml:"headers"`
		Query       string `yaml:"query"`
		Geo         string `yaml:"geo"`
		ASN         string `yaml:"asn"`
		Provider    string `yaml:"provider"`
		Country     string `yaml:"country"`
		City        string `yaml:"city"`
		OnError     string `yaml:"onError"`
		Prefix      string `yaml:"prefix"`
		Exact       string `yaml:"exact"`
		Regex       string `yaml:"regex"`
		Exists      string `yaml:"exists"`
		In          string `yaml:"in"`
		NotIn       string `yaml:"notIn"`
		All         string `yaml:"all"`
		Any         string `yaml:"any"`
		Not         string `yaml:"not"`
		RequestSize string `yaml:"requestSize"`
		Comparison  struct {
			GreaterThan        string `yaml:"greaterThan"`
			GreaterThanOrEqual string `yaml:"greaterThanOrEqual"`
			LessThan           string `yaml:"lessThan"`
			LessThanOrEqual    string `yaml:"lessThanOrEqual"`
		} `yaml:"comparison"`
		SizeUnits map[string]uint64 `yaml:"sizeUnits"`
		Allow     string            `yaml:"allow"`
		Deny      string            `yaml:"deny"`
		Status    string            `yaml:"status"`
		Code      string            `yaml:"code"`
		Limit     string            `yaml:"limit"`
	} `yaml:"waf"`
	DataProvider struct {
		Type    string `yaml:"type"`
		Path    string `yaml:"path"`
		OnError string `yaml:"onError"`
		MMDB    string `yaml:"mmdb"`
		Allow   string `yaml:"allow"`
		Deny    string `yaml:"deny"`
	} `yaml:"dataProvider"`
	Plugin struct {
		Instance                      string `yaml:"instance"`
		Capability                    string `yaml:"capability"`
		Binary                        string `yaml:"binary"`
		Args                          string `yaml:"args"`
		Env                           string `yaml:"env"`
		Capabilities                  string `yaml:"capabilities"`
		Settings                      string `yaml:"settings"`
		Limits                        string `yaml:"limits"`
		Calls                         string `yaml:"calls"`
		DefaultCalls                  int    `yaml:"defaultCalls"`
		Timeout                       string `yaml:"timeout"`
		DefaultTimeout                string `yaml:"defaultTimeout"`
		Memory                        string `yaml:"memory"`
		DefaultMemory                 string `yaml:"defaultMemory"`
		DefaultMemoryProbeInterval    string `yaml:"defaultMemoryProbeInterval"`
		Restart                       string `yaml:"restart"`
		Enabled                       string `yaml:"enabled"`
		Backoff                       string `yaml:"backoff"`
		MaxBackoff                    string `yaml:"maxBackoff"`
		DefaultRestartEnabled         bool   `yaml:"defaultRestartEnabled"`
		DefaultRestartBackoff         string `yaml:"defaultRestartBackoff"`
		DefaultRestartMaxBackoff      string `yaml:"defaultRestartMaxBackoff"`
		DefaultHealthProbeInterval    string `yaml:"defaultHealthProbeInterval"`
		DefaultHealthFailureThreshold int    `yaml:"defaultHealthFailureThreshold"`
	} `yaml:"plugin"`
	Proxy struct {
		Upstream     string `yaml:"upstream"`
		Host         string `yaml:"host"`
		HostPreserve string `yaml:"hostPreserve"`
		HostUpstream string `yaml:"hostUpstream"`
	} `yaml:"proxy"`
	Redirect struct {
		Scheme        string `yaml:"scheme"`
		Host          string `yaml:"host"`
		Path          string `yaml:"path"`
		PreserveQuery string `yaml:"preserveQuery"`
		Status        string `yaml:"status"`
	} `yaml:"redirect"`
	Rewrite struct {
		Regex       string `yaml:"regex"`
		Replacement string `yaml:"replacement"`
	} `yaml:"rewrite"`
	Headers struct {
		Request     string `yaml:"request"`
		Response    string `yaml:"response"`
		Set         string `yaml:"set"`
		SetIfAbsent string `yaml:"setIfAbsent"`
		Delete      string `yaml:"delete"`
	} `yaml:"headers"`
	Deny struct {
		Status string `yaml:"status"`
		Code   string `yaml:"code"`
	} `yaml:"deny"`
	UpstreamConfig struct {
		Targets                 string `yaml:"targets"`
		TargetAddress           string `yaml:"targetAddress"`
		TargetWeight            string `yaml:"targetWeight"`
		Balance                 string `yaml:"balance"`
		BalanceRoundRobin       string `yaml:"balanceRoundRobin"`
		BalanceLeastConnections string `yaml:"balanceLeastConnections"`
		BalanceHash             string `yaml:"balanceHash"`
		Hash                    string `yaml:"hash"`
		HashSource              string `yaml:"hashSource"`
		HashSourceIP            string `yaml:"hashSourceIp"`
		HashSourceHeader        string `yaml:"hashSourceHeader"`
		HashSourceCookie        string `yaml:"hashSourceCookie"`
		HashSourceQuery         string `yaml:"hashSourceQuery"`
		HashName                string `yaml:"hashName"`
		Retry                   string `yaml:"retry"`
		RetryAttempts           string `yaml:"retryAttempts"`
		RetryOn                 string `yaml:"retryOn"`
		RetryConnectFailure     string `yaml:"retryConnectFailure"`
		RetryTimeout            string `yaml:"retryTimeout"`
		RetryStatus502          string `yaml:"retryStatus502"`
		RetryStatus503          string `yaml:"retryStatus503"`
		RetryStatus504          string `yaml:"retryStatus504"`
	} `yaml:"upstreamConfig"`
}

type graph struct {
	variables map[string]string
	secrets   map[string]string
	documents []*yaml.Node
	root      *yaml.Node
	hasher    hash.Hash
}

func supervise(path string) (*graph, contractFile, error) {
	loaded, err := loadContractFile()
	if err != nil {
		return nil, contractFile{}, err
	}
	compiled := graph{variables: map[string]string{}, secrets: map[string]string{}, hasher: newHasher()}
	if err := collectFile(path, loaded, map[string]struct{}{}, &compiled); err != nil {
		return nil, contractFile{}, err
	}
	for _, document := range compiled.documents {
		if err := validateVariables(document, compiled.variables, loaded); err != nil {
			return nil, contractFile{}, err
		}
	}
	if err := resolveSecrets(compiled.documents, filepath.Dir(path), loaded.Secrets, loaded.SecretReference, compiled.secrets); err != nil {
		return nil, contractFile{}, err
	}
	return &compiled, loaded, nil
}

func Validate(path string) error {
	_, err := CompileGateway(path)
	return err
}

// ValidateYAML validates an in-memory management update through the same
// schema and semantic compiler as a file-backed configuration.
func ValidateYAML(document string) error {
	temporary, err := os.CreateTemp("", "liapoldus-gateway-validate-*.yaml")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.WriteString(document); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return Validate(name)
}

func loadContractFile() (contractFile, error) {
	var loaded contractFile
	contents, err := assets.Contract(assets.ConfigFields)
	if err != nil {
		return contractFile{}, err
	}
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return contractFile{}, err
	}
	return loaded, nil
}

func collectFile(path string, loaded contractFile, visited map[string]struct{}, compiled *graph) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, exists := visited[abs]; exists {
		return ErrInvalidDocument
	}
	visited[abs] = struct{}{}
	defer delete(visited, abs)

	contents, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	_, _ = compiled.hasher.Write(contents)
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}

	root := document.Content[0]
	if compiled.root == nil {
		compiled.root = root
	}
	compiled.documents = append(compiled.documents, root)
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if !contains(loaded.Root, key.Value) {
			return ErrUnknownField
		}
		switch key.Value {
		case loaded.Includes:
			if err := collectIncludes(abs, value, loaded, visited, compiled); err != nil {
				return err
			}
		case loaded.Listeners:
			if err := validateListeners(value, loaded); err != nil {
				return err
			}
		case loaded.Variables:
			if err := collectVariables(value, compiled.variables); err != nil {
				return err
			}
		}
	}
	return validateSchema(root)
}

func collectIncludes(parent string, node *yaml.Node, loaded contractFile, visited map[string]struct{}, compiled *graph) error {
	if node.Kind != yaml.SequenceNode {
		return ErrInvalidDocument
	}
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return ErrInvalidDocument
		}
		pattern := filepath.Join(filepath.Dir(parent), item.Value)
		files, err := expandInclude(pattern)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return ErrInvalidDocument
		}
		for _, file := range files {
			if err := collectFile(file, loaded, visited, compiled); err != nil {
				return err
			}
		}
	}
	return nil
}

func expandInclude(pattern string) ([]string, error) {
	index := strings.IndexAny(pattern, "*?[")
	if index < 0 {
		return []string{pattern}, nil
	}
	base := pattern[:index]
	if segment := strings.LastIndex(base, string(filepath.Separator)); segment >= 0 {
		base = base[:segment]
	} else if base == "" {
		base = "."
	}
	if base == "" {
		base = string(filepath.Separator)
	}
	parts := strings.Split(filepath.ToSlash(pattern[index:]), "/")
	var matches []string
	err := filepath.WalkDir(base, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(base, current)
		if relErr != nil {
			return relErr
		}
		if matchGlobParts(strings.Split(filepath.ToSlash(relative), "/"), parts) {
			matches = append(matches, current)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func matchGlobParts(value []string, pattern []string) bool {
	rows := make([][]bool, len(pattern)+1)
	for row := range rows {
		rows[row] = make([]bool, len(value)+1)
	}
	rows[0][0] = true
	for row, item := range pattern {
		if item == "**" {
			for column, set := range rows[row] {
				if !set {
					continue
				}
				for next := column; next <= len(value); next++ {
					rows[row+1][next] = true
				}
			}
			continue
		}
		for column := 1; column <= len(value); column++ {
			if !rows[row][column-1] {
				continue
			}
			matched, matchErr := path.Match(item, value[column-1])
			if matchErr != nil || !matched {
				continue
			}
			rows[row+1][column] = true
		}
	}
	return rows[len(pattern)][len(value)]
}

func hasGlobMeta(value string) bool {
	return strings.ContainsAny(value, "*?[")
}

type secretResolver struct {
	env  map[string]string
	file map[string]string
}

func newSecretResolver() *secretResolver {
	return &secretResolver{env: map[string]string{}, file: map[string]string{}}
}

func (resolver *secretResolver) resolve(reference, baseDir string, words contractSecretReference) (string, error) {
	if strings.HasPrefix(reference, words.EnvPrefix) {
		name := strings.TrimPrefix(reference, words.EnvPrefix)
		if value, found := resolver.env[name]; found {
			return value, nil
		}
		value := os.Getenv(name)
		resolver.env[name] = value
		return value, nil
	}
	if strings.HasPrefix(reference, words.FilePrefix) {
		path := strings.TrimPrefix(reference, words.FilePrefix)
		if value, found := resolver.file[path]; found {
			return value, nil
		}
		target := path
		if !filepath.IsAbs(target) {
			target = filepath.Join(baseDir, target)
		}
		contents, err := os.ReadFile(target)
		if err != nil {
			return "", ErrInvalidDocument
		}
		value := strings.TrimRight(string(contents), "\n")
		resolver.file[path] = value
		return value, nil
	}
	return reference, nil
}

func resolveSecrets(documents []*yaml.Node, baseDir string, key string, words contractSecretReference, secrets map[string]string) error {
	resolver := newSecretResolver()
	for _, document := range documents {
		node := mappingNode(document, key)
		if node == nil || node.Kind != yaml.MappingNode {
			continue
		}
		for index := 0; index < len(node.Content); index += 2 {
			value := node.Content[index+1]
			if value.Kind != yaml.ScalarNode {
				continue
			}
			resolved, err := resolver.resolve(value.Value, baseDir, words)
			if err != nil {
				return err
			}
			secrets[node.Content[index].Value] = resolved
		}
	}
	return nil
}

func validateSchema(root *yaml.Node) error {
	var raw any
	if err := root.Decode(&raw); err != nil {
		return err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		return err
	}
	contents, err := assets.Contract(assets.GatewaySchema)
	if err != nil {
		return err
	}
	var schemaDocument struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(contents, &schemaDocument); err != nil {
		return err
	}
	var schemaValue any
	if err := json.Unmarshal(contents, &schemaValue); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaDocument.ID, schemaValue); err != nil {
		return err
	}
	schema, err := compiler.Compile(schemaDocument.ID)
	if err != nil {
		return err
	}
	return schema.Validate(instance)
}

func collectVariables(node *yaml.Node, variables map[string]string) error {
	if node.Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, value := node.Content[index], node.Content[index+1]
		if value.Kind != yaml.ScalarNode {
			return ErrInvalidDocument
		}
		if _, exists := variables[name.Value]; exists {
			return ErrInvalidDocument
		}
		variables[name.Value] = value.Value
	}
	return nil
}

func validateVariables(node *yaml.Node, variables map[string]string, loaded contractFile) error {
	if node.Kind == yaml.ScalarNode {
		return validateScalar(node.Value, variables, loaded)
	}
	for _, child := range node.Content {
		if err := validateVariables(child, variables, loaded); err != nil {
			return err
		}
	}
	return nil
}

func validateScalar(value string, variables map[string]string, loaded contractFile) error {
	for remainder := value; ; {
		start := strings.Index(remainder, loaded.Substitution.Open)
		if start < 0 {
			return nil
		}
		remainder = remainder[start+len(loaded.Substitution.Open):]
		end := strings.Index(remainder, loaded.Substitution.Close)
		if end < 0 {
			return ErrInvalidDocument
		}
		name := remainder[:end]
		if _, exists := variables[name]; !exists && !isRewriteCapture(name, value) {
			return ErrInvalidDocument
		}
		remainder = remainder[end+len(loaded.Substitution.Close):]
	}
}

func isRewriteCapture(name, value string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return strings.Contains(value, "/")
}

func validateListeners(node *yaml.Node, loaded contractFile) error {
	if node.Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}
	for index := 1; index < len(node.Content); index += 2 {
		listener := node.Content[index]
		if listener.Kind != yaml.MappingNode {
			return ErrInvalidDocument
		}
		for field := 0; field < len(listener.Content); field += 2 {
			if !contains(loaded.Listener, listener.Content[field].Value) {
				return ErrUnknownField
			}
		}
	}
	return nil
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func IsUnknownField(err error) bool { return errors.Is(err, ErrUnknownField) }
