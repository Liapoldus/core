package models

type BalanceMode uint8

const (
	BalanceRoundRobin BalanceMode = iota + 1
	BalanceLeastConnections
	BalanceHash
)
