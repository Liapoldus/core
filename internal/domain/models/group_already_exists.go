package models

type GroupAlreadyExists struct {
	Message string
}

func (problem GroupAlreadyExists) Error() string {
	return problem.Message
}
