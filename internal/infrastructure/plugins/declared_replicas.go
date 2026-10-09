package plugins

import (
	"net/http"
	"net/url"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

// RegisteredReplicaTarget is a live lease snapshot reduced to the values Core
// needs to dial it. TLS material remains Core-owned; the SDK owns the protocol.
type RegisteredReplicaTarget struct {
	InstanceID                 string
	ReplicaID                  string
	Endpoint                   *url.URL
	ExpectedCommonName         string
	ExpectedResourceIdentifier string
	// Transport carries the exact TLS configuration Core verified for this
	// replica. It is required: a client without verified transport would accept
	// whatever the network presented.
	Transport *http.Transport
}

// RegisteredReplicaFanouts builds one SDK control client per live lease,
// grouped by instance, and returns a release function for their idle connections.
func RegisteredReplicaFanouts(replicas []RegisteredReplicaTarget) (map[string]SDKReloadClient, func(), error) {
	clients := make(map[string]SDKReloadClient)
	opened := make([]*http.Transport, 0, len(replicas))
	contract, err := sdkinfrastructure.LoadHTTPContract()
	if err != nil {
		return nil, nil, err
	}
	release := func() {
		for _, transport := range opened {
			transport.CloseIdleConnections()
		}
	}
	for _, replica := range replicas {
		if replica.InstanceID == "" || replica.ReplicaID == "" || replica.Endpoint == nil || replica.Transport == nil {
			release()
			return nil, nil, ErrPluginUnavailable
		}
		// The SDK is the authority on the exact peer identity. Core validated the
		// same rules at registration time, so disagreement here means the identity is
		// unusable rather than something to work around.
		peer, err := sdkmodels.NewPeerIdentity(replica.ExpectedCommonName, replica.ExpectedResourceIdentifier)
		if err != nil {
			release()
			return nil, nil, err
		}
		replica.Transport.ResponseHeaderTimeout = time.Duration(contract.Deadlines.PluginReloadSeconds) * time.Second
		client, err := sdkinfrastructure.NewPluginClient(
			contract,
			replica.Endpoint.String(),
			&declaredReplicaTransport{client: &http.Client{Transport: replica.Transport}},
			peer,
		)
		if err != nil {
			release()
			return nil, nil, err
		}
		opened = append(opened, replica.Transport)
		fanout, ok := clients[replica.InstanceID].(*SDKReloadFanout)
		if !ok {
			fanout = &SDKReloadFanout{InstanceID: replica.InstanceID}
			clients[replica.InstanceID] = fanout
		}
		fanout.Replicas = append(fanout.Replicas, SDKReloadReplicaClient{
			ReplicaID: replica.ReplicaID,
			Client:    client,
		})
	}
	return clients, release, nil
}

// declaredReplicaTransport adapts a verified HTTPS client to the Plugin SDK
// control transport port. It moves bytes and nothing else: route resolution,
// media types, limits and outcome classification stay owned by the SDK client.
type declaredReplicaTransport struct {
	client *http.Client
}

func (transport *declaredReplicaTransport) Do(request *http.Request) (*http.Response, error) {
	return transport.client.Do(request)
}

// DoArtifact uses the same pinned peer identity and verified mTLS transport as
// every other Plugin SDK control call while preserving the long-lived streaming
// request body. The SDK controls the deadline and multipart contract.
func (transport *declaredReplicaTransport) DoArtifact(request *http.Request) (*http.Response, error) {
	return transport.client.Do(request)
}

// DoAdminAction preserves the SDK's action deadline instead of routing the
// request through a shorter generic control-call timeout.
func (transport *declaredReplicaTransport) DoAdminAction(request *http.Request) (*http.Response, error) {
	return transport.client.Do(request)
}
