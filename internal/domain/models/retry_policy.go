package models

type RetryPolicy struct {
	Attempts   int
	Conditions []RetryCondition
}
