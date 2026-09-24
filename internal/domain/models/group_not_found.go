package models

type GroupNotFound struct {
	Message string
}

func (problem GroupNotFound) Error() string {
	return problem.Message
}
