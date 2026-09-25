package plugins

import "github.com/Liapoldus/pluginprotocol/pluginv1"

func ManifestSupportsMode(manifest *pluginv1.Manifest, capability string, requiredMode pluginv1.InvocationMode) bool {
	if manifest == nil || capability == "" || !supportedInvocationMode(requiredMode) {
		return false
	}
	for _, descriptor := range manifest.GetCapabilityDescriptors() {
		if descriptor.GetCapability() != capability {
			continue
		}
		for _, mode := range descriptor.GetModes() {
			if mode == requiredMode {
				return true
			}
		}
		return false
	}
	return false
}

func ManifestSupportsCall(manifest *pluginv1.Manifest, capability string) bool {
	return ManifestSupportsMode(manifest, capability, pluginv1.InvocationMode_INVOCATION_MODE_CALL)
}
