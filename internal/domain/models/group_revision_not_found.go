package models

type GroupRevisionNotFound struct {
	Message string
}

func (problem GroupRevisionNotFound) Error() string {
	return problem.Message
}
