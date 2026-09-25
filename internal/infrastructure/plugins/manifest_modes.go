package plugins

import "github.com/Liapoldus/pluginprotocol/pluginv1"

func ManifestSupportsCall(manifest *pluginv1.Manifest, capability string) bool {
	if manifest == nil || capability == "" {
		return false
	}
	for _, descriptor := range manifest.GetCapabilityDescriptors() {
		if descriptor.GetCapability() != capability {
			continue
		}
		for _, mode := range descriptor.GetModes() {
			if mode == pluginv1.InvocationMode_INVOCATION_MODE_CALL {
				return true
			}
		}
		return false
	}
	return false
}
