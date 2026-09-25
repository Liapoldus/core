package plugins

import (
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/encoding/protojson"
)

type ManifestInventory struct {
	Capabilities []string
	Descriptors  []ManifestCapabilityDescriptor
}

type ManifestCapabilityDescriptor struct {
	Capability string
	Modes      []string
}

func ParseManifestInventory(instanceID string, manifestJSON []byte) (ManifestInventory, error) {
	manifest := new(pluginv1.Manifest)
	if err := protojson.Unmarshal(manifestJSON, manifest); err != nil || ValidateManifest(manifest, nil) != nil || manifest.GetName() != instanceID {
		return ManifestInventory{}, ErrProtocolViolation
	}

	inventory := ManifestInventory{
		Capabilities: append([]string(nil), manifest.GetCapabilities()...),
		Descriptors:  make([]ManifestCapabilityDescriptor, 0, len(manifest.GetCapabilityDescriptors())),
	}
	for _, descriptor := range manifest.GetCapabilityDescriptors() {
		modes := make([]string, 0, len(descriptor.GetModes()))
		for _, mode := range descriptor.GetModes() {
			modes = append(modes, mode.String())
		}
		inventory.Descriptors = append(inventory.Descriptors, ManifestCapabilityDescriptor{
			Capability: descriptor.GetCapability(),
			Modes:      modes,
		})
	}
	return inventory, nil
}
