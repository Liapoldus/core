package models

type OperationNotFound struct {
	Message string
}

func (problem OperationNotFound) Error() string {
	return problem.Message
}
