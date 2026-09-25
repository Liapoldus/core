package models

type IdempotencyConflict struct {
	Message string
}

func (conflict IdempotencyConflict) Error() string {
	return conflict.Message
}
