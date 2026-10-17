package services

import (
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/service-sdk/v2/infrastructure"
)

// SDKHTTPContract is the Service SDK contract consumed by Core's control-plane
// adapters. Keeping the external SDK import behind infrastructure/services
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
