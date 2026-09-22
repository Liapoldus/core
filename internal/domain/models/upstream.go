package models

type Upstream struct {
	Targets []UpstreamTarget
	Balance BalanceMode
	Hash    HashPolicy
	Retry   RetryPolicy
}
