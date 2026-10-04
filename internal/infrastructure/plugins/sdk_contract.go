package plugins

import sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"

// SDKHTTPContract is the Plugin SDK contract consumed by Core's control-plane
// adapters. Keeping the external SDK import behind infrastructure/plugins
// prevents presentation bootstrap from depending on a transport library.
type SDKHTTPContract = sdkinfrastructure.HTTPContract

func LoadSDKHTTPContract() (SDKHTTPContract, error) {
	return sdkinfrastructure.LoadHTTPContract()
}
