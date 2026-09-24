package models

type GroupRevisionConflict struct {
	Expected *string
	Actual   *string
	Message  string
}

func (conflict GroupRevisionConflict) Error() string {
	return conflict.Message
}
