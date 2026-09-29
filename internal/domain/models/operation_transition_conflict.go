package models

type OperationTransitionConflict struct {
	Message string
}

func (conflict OperationTransitionConflict) Error() string {
	return conflict.Message
}
