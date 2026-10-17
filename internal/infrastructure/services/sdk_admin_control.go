package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/Liapoldus/service-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/service-sdk/v2/infrastructure"
)

// SDKAdminReplicaClient is the product-neutral subset of the Service SDK client
// used by Core's Management API. It deliberately exposes opaque JSON documents
// and byte streams rather than service-specific contracts.
type SDKAdminReplicaClient interface {
	AdminSurface(context.Context) (sdkinfrastructure.AdminSurfaceDocument, error)
	AdminAction(context.Context, models.AdminActionInvocation, []byte) (sdkinfrastructure.AdminActionResult, error)
	ArtifactStream(context.Context, models.ArtifactInvocation, []byte, string, io.ReadCloser) (sdkinfrastructure.ArtifactStreamResult, error)
}

// ServiceAdminSurface is the exact service-owned surface document bound to its
// registered instance. RawMessage keeps the descriptor a JSON document in the
// Management API response instead of double-encoding it as a JSON string.
type ServiceAdminSurface struct {
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

// SDKAdminControl forwards generic Admin Surface requests only to live,
// authenticated registrations acknowledged at the active generation.
type SDKAdminControl struct {
	Instances          func(context.Context) ([]string, error)
	ResolveFanout      func(context.Context, string) (*SDKReloadFanout, func(), bool, error)
	EligibleReplicaIDs func(context.Context, string) ([]string, error)
	HTTPContract       sdkinfrastructure.HTTPContract
}

func (control *SDKAdminControl) AdminLimits() SDKAdminLimits {
	if control == nil {
		return SDKAdminLimits{}
	}
	artifact := control.HTTPContract.Service.ArtifactStream
	if len(artifact.Parts) < 2 {
		return SDKAdminLimits{}
	}
	return SDKAdminLimits{
		JSONRequestBytes:   control.HTTPContract.Service.AdminAction.MaximumRequestBytes,
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

// Surfaces reads every live replica and requires exact document and digest
// agreement. A partial or inconsistent view is not advertised to operators.
func (control *SDKAdminControl) Surfaces(ctx context.Context) ([]ServiceAdminSurface, error) {
	if control == nil || control.Instances == nil || control.ResolveFanout == nil {
		return nil, ErrServiceUnavailable
	}
	instanceIDs, err := control.Instances(ctx)
	if err != nil {
		return nil, ErrServiceUnavailable
	}
	sort.Strings(instanceIDs)
	result := make([]ServiceAdminSurface, 0, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		fanout, release, found, err := control.ResolveFanout(ctx, instanceID)
		if err != nil || !found || fanout == nil || len(fanout.Replicas) == 0 {
			if release != nil {
				release()
			}
			return nil, ErrServiceUnavailable
		}
		if release != nil {
			defer release()
		}
		var agreed *sdkinfrastructure.AdminSurfaceDocument
		for _, replica := range fanout.Replicas {
			client, ok := replica.Client.(SDKAdminReplicaClient)
			if !ok {
				return nil, ErrServiceUnavailable
			}
			document, err := client.AdminSurface(ctx)
			if err != nil || !validSurfaceDigest(document, control.HTTPContract.Service.AdminSurface.DigestAlgorithm) {
				return nil, ErrServiceUnavailable
			}
			if agreed == nil {
				copy := document
				agreed = &copy
				continue
			}
			if agreed.SHA256 != document.SHA256 || !bytes.Equal(agreed.Bytes, document.Bytes) {
				return nil, ErrServiceUnavailable
			}
		}
		if agreed == nil {
			return nil, ErrServiceUnavailable
		}
		result = append(result, ServiceAdminSurface{InstanceID: instanceID,
			Descriptor: append(json.RawMessage(nil), agreed.Bytes...), SHA256: agreed.SHA256})
	}
	return result, nil
}

func (control *SDKAdminControl) SurfaceDigest(ctx context.Context, instanceID string) (string, error) {
	if control == nil || control.ResolveFanout == nil || instanceID == "" {
		return "", ErrServiceUnavailable
	}
	fanout, release, found, err := control.ResolveFanout(ctx, instanceID)
	if err != nil || !found || fanout == nil || len(fanout.Replicas) == 0 {
		if release != nil {
			release()
		}
		return "", ErrServiceUnavailable
	}
	if release != nil {
		defer release()
	}
	var agreed *sdkinfrastructure.AdminSurfaceDocument
	for _, replica := range fanout.Replicas {
		client, ok := replica.Client.(SDKAdminReplicaClient)
		if !ok {
			return "", ErrServiceUnavailable
		}
		document, err := client.AdminSurface(ctx)
		if err != nil || !validSurfaceDigest(document, control.HTTPContract.Service.AdminSurface.DigestAlgorithm) {
			return "", ErrServiceUnavailable
		}
		if agreed == nil {
			copy := document
			agreed = &copy
			continue
		}
		if agreed.SHA256 != document.SHA256 || !bytes.Equal(agreed.Bytes, document.Bytes) {
			return "", ErrServiceUnavailable
		}
	}
	if agreed == nil {
		return "", ErrServiceUnavailable
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

func (control *SDKAdminControl) selectedClient(ctx context.Context, instanceID string) (SDKAdminReplicaClient, func(), error) {
	if control == nil || control.EligibleReplicaIDs == nil || control.ResolveFanout == nil {
		return nil, nil, ErrServiceUnavailable
	}
	client, release, found, err := control.ResolveFanout(ctx, instanceID)
	if err != nil || !found || client == nil {
		if release != nil {
			release()
		}
		return nil, nil, ErrServiceUnavailable
	}
	eligible, err := control.EligibleReplicaIDs(ctx, instanceID)
	if err != nil || len(eligible) == 0 {
		if release != nil {
			release()
		}
		return nil, nil, ErrServiceUnavailable
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
			return adminClient, release, nil
		}
	}
	if release != nil {
		release()
	}
	return nil, nil, ErrServiceUnavailable
}

func (control *SDKAdminControl) Action(ctx context.Context, invocation models.AdminActionInvocation, input []byte) (sdkinfrastructure.AdminActionResult, error) {
	client, release, err := control.selectedClient(ctx, invocation.InstanceID)
	if err != nil {
		return sdkinfrastructure.AdminActionResult{}, err
	}
	if release != nil {
		defer release()
	}
	result, err := client.AdminAction(ctx, invocation, input)
	if err != nil {
		return sdkinfrastructure.AdminActionResult{}, ErrServiceUnavailable
	}
	return result, nil
}

func (control *SDKAdminControl) Artifact(ctx context.Context, invocation models.ArtifactInvocation, metadata []byte, contentType string, artifact io.ReadCloser) (sdkinfrastructure.ArtifactStreamResult, error) {
	client, release, err := control.selectedClient(ctx, invocation.InstanceID)
	if err != nil {
		_ = artifact.Close()
		return sdkinfrastructure.ArtifactStreamResult{}, ErrServiceUnavailable
	}
	if release != nil {
		defer release()
	}
	result, err := client.ArtifactStream(ctx, invocation, metadata, contentType, artifact)
	if err != nil {
		return sdkinfrastructure.ArtifactStreamResult{}, ErrServiceUnavailable
	}
	return result, nil
}
