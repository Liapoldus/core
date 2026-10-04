package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

// SDKAdminReplicaClient is the product-neutral subset of the Plugin SDK client
// used by Core's Management API. It deliberately exposes opaque JSON documents
// and byte streams rather than plugin-specific contracts.
type SDKAdminReplicaClient interface {
	AdminSurface(context.Context) (sdkinfrastructure.AdminSurfaceDocument, error)
	AdminAction(context.Context, models.AdminActionInvocation, []byte) (sdkinfrastructure.AdminActionResult, error)
	ArtifactStream(context.Context, models.ArtifactInvocation, []byte, string, io.ReadCloser) (sdkinfrastructure.ArtifactStreamResult, error)
}

// PluginAdminSurface is the exact plugin-owned surface document bound to its
// declared instance. RawMessage keeps the descriptor a JSON document in the
// Management API response instead of double-encoding it as a JSON string.
type PluginAdminSurface struct {
	InstanceID string          `json:"instanceId"`
	Descriptor json.RawMessage `json:"descriptor"`
	SHA256     string          `json:"sha256"`
}

type SDKAdminLimits struct {
	JSONRequestBytes   int64
	ArtifactBytes      int64
	MinimumArtifact    int64
	MetadataBytes      int64
	MultipartBytes     int64
	MaximumRequest     int64
	MetadataPartName   string
	ArtifactPartName   string
	MultipartMediaType string
	MetadataMediaType  string
}

// SDKAdminControl forwards generic Admin Surface requests only to operator-
// declared replicas. EligibleReplicaIDs must return replicas observed at the
// current active generation; Core never retries a request after dispatch.
type SDKAdminControl struct {
	Clients            map[string]SDKReloadClient
	EligibleReplicaIDs func(context.Context, string) ([]string, error)
	HTTPContract       sdkinfrastructure.HTTPContract
}

func (control *SDKAdminControl) AdminLimits() SDKAdminLimits {
	if control == nil {
		return SDKAdminLimits{}
	}
	artifact := control.HTTPContract.Plugin.ArtifactStream
	if len(artifact.Parts) < 2 {
		return SDKAdminLimits{}
	}
	return SDKAdminLimits{
		JSONRequestBytes:   control.HTTPContract.Plugin.AdminAction.MaximumRequestBytes,
		ArtifactBytes:      artifact.MaximumArtifactBytes,
		MinimumArtifact:    artifact.MinimumArtifactBytes,
		MetadataBytes:      artifact.MaximumMetadataBytes,
		MultipartBytes:     artifact.MaximumMultipartOverheadBytes,
		MaximumRequest:     artifact.MaximumRequestBytes,
		MetadataPartName:   artifact.Parts[0],
		ArtifactPartName:   artifact.Parts[1],
		MultipartMediaType: artifact.MediaType,
		MetadataMediaType:  artifact.MetadataMediaType,
	}
}

// Surfaces reads every declared replica and requires exact document and digest
// agreement. A partial or inconsistent view is not advertised to operators.
func (control *SDKAdminControl) Surfaces(ctx context.Context) ([]PluginAdminSurface, error) {
	if control == nil || len(control.Clients) == 0 {
		return nil, ErrPluginUnavailable
	}
	instanceIDs := make([]string, 0, len(control.Clients))
	for instanceID := range control.Clients {
		instanceIDs = append(instanceIDs, instanceID)
	}
	sort.Strings(instanceIDs)
	result := make([]PluginAdminSurface, 0, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		fanout, ok := control.Clients[instanceID].(*SDKReloadFanout)
		if !ok || len(fanout.Replicas) == 0 {
			return nil, ErrPluginUnavailable
		}
		var agreed *sdkinfrastructure.AdminSurfaceDocument
		for _, replica := range fanout.Replicas {
			client, ok := replica.Client.(SDKAdminReplicaClient)
			if !ok {
				return nil, ErrPluginUnavailable
			}
			document, err := client.AdminSurface(ctx)
			if err != nil || !validSurfaceDigest(document, control.HTTPContract.Plugin.AdminSurface.DigestAlgorithm) {
				return nil, ErrPluginUnavailable
			}
			if agreed == nil {
				copy := document
				agreed = &copy
				continue
			}
			if agreed.SHA256 != document.SHA256 || !bytes.Equal(agreed.Bytes, document.Bytes) {
				return nil, ErrPluginUnavailable
			}
		}
		if agreed == nil {
			return nil, ErrPluginUnavailable
		}
		result = append(result, PluginAdminSurface{InstanceID: instanceID,
			Descriptor: append(json.RawMessage(nil), agreed.Bytes...), SHA256: agreed.SHA256})
	}
	return result, nil
}

func (control *SDKAdminControl) SurfaceDigest(ctx context.Context, instanceID string) (string, error) {
	if control == nil || instanceID == "" {
		return "", ErrPluginUnavailable
	}
	fanout, ok := control.Clients[instanceID].(*SDKReloadFanout)
	if !ok || len(fanout.Replicas) == 0 {
		return "", ErrPluginUnavailable
	}
	var agreed *sdkinfrastructure.AdminSurfaceDocument
	for _, replica := range fanout.Replicas {
		client, ok := replica.Client.(SDKAdminReplicaClient)
		if !ok {
			return "", ErrPluginUnavailable
		}
		document, err := client.AdminSurface(ctx)
		if err != nil || !validSurfaceDigest(document, control.HTTPContract.Plugin.AdminSurface.DigestAlgorithm) {
			return "", ErrPluginUnavailable
		}
		if agreed == nil {
			copy := document
			agreed = &copy
			continue
		}
		if agreed.SHA256 != document.SHA256 || !bytes.Equal(agreed.Bytes, document.Bytes) {
			return "", ErrPluginUnavailable
		}
	}
	if agreed == nil {
		return "", ErrPluginUnavailable
	}
	return agreed.SHA256, nil
}

func validSurfaceDigest(document sdkinfrastructure.AdminSurfaceDocument, algorithm string) bool {
	if len(document.Bytes) == 0 || !json.Valid(document.Bytes) || document.SHA256 == "" {
		return false
	}
	prefix, encodedDigest, found := strings.Cut(document.SHA256, ":")
	expectedPrefix := strings.ToLower(strings.ReplaceAll(algorithm, "-", ""))
	if !found || expectedPrefix == "" || prefix != expectedPrefix {
		return false
	}
	digest := sha256.Sum256(document.Bytes)
	return hex.EncodeToString(digest[:]) == encodedDigest
}

func (control *SDKAdminControl) selectedClient(ctx context.Context, instanceID string) (SDKAdminReplicaClient, error) {
	if control == nil || control.EligibleReplicaIDs == nil {
		return nil, ErrPluginUnavailable
	}
	client, ok := control.Clients[instanceID].(*SDKReloadFanout)
	if !ok || client == nil {
		return nil, ErrPluginUnavailable
	}
	eligible, err := control.EligibleReplicaIDs(ctx, instanceID)
	if err != nil || len(eligible) == 0 {
		return nil, ErrPluginUnavailable
	}
	sort.Strings(eligible)
	allowed := make(map[string]struct{}, len(eligible))
	for _, replicaID := range eligible {
		allowed[replicaID] = struct{}{}
	}
	replicas := append([]SDKReloadReplicaClient(nil), client.Replicas...)
	sort.Slice(replicas, func(i, j int) bool { return replicas[i].ReplicaID < replicas[j].ReplicaID })
	for _, replica := range replicas {
		if _, ok := allowed[replica.ReplicaID]; !ok {
			continue
		}
		adminClient, ok := replica.Client.(SDKAdminReplicaClient)
		if ok {
			return adminClient, nil
		}
	}
	return nil, ErrPluginUnavailable
}

func (control *SDKAdminControl) Action(ctx context.Context, invocation models.AdminActionInvocation, input []byte) (sdkinfrastructure.AdminActionResult, error) {
	client, err := control.selectedClient(ctx, invocation.InstanceID)
	if err != nil {
		return sdkinfrastructure.AdminActionResult{}, err
	}
	result, err := client.AdminAction(ctx, invocation, input)
	if err != nil {
		return sdkinfrastructure.AdminActionResult{}, ErrPluginUnavailable
	}
	return result, nil
}

func (control *SDKAdminControl) Artifact(ctx context.Context, invocation models.ArtifactInvocation, metadata []byte, contentType string, artifact io.ReadCloser) (sdkinfrastructure.ArtifactStreamResult, error) {
	client, err := control.selectedClient(ctx, invocation.InstanceID)
	if err != nil {
		_ = artifact.Close()
		return sdkinfrastructure.ArtifactStreamResult{}, err
	}
	result, err := client.ArtifactStream(ctx, invocation, metadata, contentType, artifact)
	if err != nil {
		return sdkinfrastructure.ArtifactStreamResult{}, ErrPluginUnavailable
	}
	return result, nil
}
