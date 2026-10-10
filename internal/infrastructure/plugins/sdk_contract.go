package plugins

import (
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/v2/infrastructure"
)

// SDKHTTPContract is the Plugin SDK contract consumed by Core's control-plane
// adapters. Keeping the external SDK import behind infrastructure/plugins
// prevents presentation bootstrap from depending on a transport library.
type SDKHTTPContract = sdkinfrastructure.HTTPContract

type SDKReplicaLifecycleContract = sdkinfrastructure.ReplicaLifecycleContract

type SDKReplicaRegistrationRequest = sdkmodels.ReplicaRegistrationRequest

type SDKPeerDirectoryPollContract = sdkinfrastructure.PeerDirectoryPollContract

func LoadSDKHTTPContract() (SDKHTTPContract, error) {
	return sdkinfrastructure.LoadHTTPContract()
}

func LoadSDKReplicaLifecycleContract() (SDKReplicaLifecycleContract, error) {
	return sdkinfrastructure.LoadReplicaLifecycleContract()
}

func LoadSDKPeerDirectoryPollContract() (SDKPeerDirectoryPollContract, error) {
	return sdkinfrastructure.LoadPeerDirectoryPollContract()
}
