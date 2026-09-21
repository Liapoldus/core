package main

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

var errUnknownField = errors.New("unknown configuration field")

var allowedRootFields = map[string]struct{}{
	"includes": {}, "variables": {}, "secrets": {}, "registry": {}, "sites": {},
	"listeners": {}, "upstreams": {}, "tlsProfiles": {}, "tlsIssuers": {},
	"authPolicies": {}, "dataProviders": {}, "wafPolicies": {}, "rateLimits": {},
	"captchaProviders": {}, "plugins": {}, "management": {}, "logging": {},
	"metrics": {}, "tracing": {},
}

func validateConfig(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("root must be a mapping")
	}

	root := document.Content[0]
	for index := 0; index < len(root.Content); index += 2 {
		field := root.Content[index].Value
		if _, allowed := allowedRootFields[field]; !allowed {
			return errUnknownField
		}
	}
	return nil
}
