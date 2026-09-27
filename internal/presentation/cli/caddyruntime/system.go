package caddyruntime

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/Liapoldus/core/internal/infrastructure/config"
)

func NewSystemCaddyActivator(bootstrap config.BootstrapConfig, runtimeBindings RuntimeBindings, pluginBindings []PluginDispatchBinding, unavailable string, policyVersion int) CaddyRuntime {
	var activator *lazyCaddyActivator
	var replace func(context.Context, CaddyRuntime, []byte, []PluginDispatchBinding) error
	switch bootstrap.CaddyVariant {
	case bootstrap.CaddyEmbeddedVariant:
		replace = runtimeBindings.ReplaceEmbeddedCaddy
	case bootstrap.CaddyExternalVariant:
		replace = runtimeBindings.ReplaceExternalCaddy
	}
	validate := func(source []byte) error {
		switch bootstrap.CaddyVariant {
		case bootstrap.CaddyEmbeddedVariant:
			if runtimeBindings.ValidateEmbeddedCaddy == nil {
				return errors.New(unavailable)
			}
			return runtimeBindings.ValidateEmbeddedCaddy(source, pluginBindings)
		case bootstrap.CaddyExternalVariant:
			if runtimeBindings.ValidateExternalCaddy == nil {
				return errors.New(unavailable)
			}
			return runtimeBindings.ValidateExternalCaddy(bootstrap.CaddyBinary, source, pluginBindings)
		default:
			return errors.New(unavailable)
		}
	}
	start := func(source []byte) (CaddyRuntime, error) {
		switch bootstrap.CaddyVariant {
		case bootstrap.CaddyEmbeddedVariant:
			if runtimeBindings.StartEmbeddedCaddy == nil {
				return nil, errors.New(unavailable)
			}
			return runtimeBindings.StartEmbeddedCaddy(source, activator.bindings)
		case bootstrap.CaddyExternalVariant:
			if bootstrap.CaddyBinary == "" || bootstrap.CaddyExpectedBuildID == "" || runtimeBindings.StartExternalCaddy == nil {
				return nil, errors.New(unavailable)
			}
			return runtimeBindings.StartExternalCaddy(bootstrap.CaddyBinary, bootstrap.CaddyExpectedBuildID, filepath.Dir(bootstrap.StatePath), source, pluginBindings)
		default:
			return nil, errors.New(unavailable)
		}
	}
	activator = newLazyCaddyActivator(validate, start, unavailable, pluginBindings, replace, policyVersion)
	return activator
}
