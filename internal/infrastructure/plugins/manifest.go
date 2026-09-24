package plugins

import (
	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

func ValidateManifest(manifest *pluginv1.Manifest, required []string) error {
	if manifest == nil || manifest.GetName() == "" || manifest.GetProtocolVersion() != pluginprotocol.ProtocolVersion {
		return ErrProtocolViolation
	}

	capabilities := make(map[string]struct{}, len(manifest.GetCapabilities()))
	for _, capability := range manifest.GetCapabilities() {
		if capability == "" {
			return ErrProtocolViolation
		}
		if _, exists := capabilities[capability]; exists {
			return ErrProtocolViolation
		}
		capabilities[capability] = struct{}{}
	}
	if len(capabilities) == 0 {
		return ErrProtocolViolation
	}

	descriptors := make(map[string]struct{}, len(manifest.GetCapabilityDescriptors()))
	for _, descriptor := range manifest.GetCapabilityDescriptors() {
		if descriptor == nil || descriptor.GetCapability() == "" || len(descriptor.GetModes()) == 0 {
			return ErrProtocolViolation
		}
		if _, exists := capabilities[descriptor.GetCapability()]; !exists {
			return ErrProtocolViolation
		}
		if _, exists := descriptors[descriptor.GetCapability()]; exists {
			return ErrProtocolViolation
		}
		descriptors[descriptor.GetCapability()] = struct{}{}

		modes := make(map[pluginv1.InvocationMode]struct{}, len(descriptor.GetModes()))
		for _, mode := range descriptor.GetModes() {
			if !supportedInvocationMode(mode) {
				return ErrProtocolViolation
			}
			if _, exists := modes[mode]; exists {
				return ErrProtocolViolation
			}
			modes[mode] = struct{}{}
		}
	}
	if len(descriptors) != len(capabilities) {
		return ErrProtocolViolation
	}

	for _, capability := range required {
		if _, exists := capabilities[capability]; !exists {
			return ErrProtocolViolation
		}
	}
	return nil
}

func supportedInvocationMode(mode pluginv1.InvocationMode) bool {
	switch mode {
	case pluginv1.InvocationMode_INVOCATION_MODE_CALL,
		pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM,
		pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET,
		pluginv1.InvocationMode_INVOCATION_MODE_SSE,
		pluginv1.InvocationMode_INVOCATION_MODE_TCP,
		pluginv1.InvocationMode_INVOCATION_MODE_UDP:
		return true
	default:
		return false
	}
}
