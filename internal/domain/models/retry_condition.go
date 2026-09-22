package models

type RetryCondition uint8

const (
	RetryConnectFailure RetryCondition = iota + 1
	RetryTimeout
	RetryStatus502
	RetryStatus503
	RetryStatus504
)
