package models

type GroupRevisionPageError struct {
	Message string
}

func (problem GroupRevisionPageError) Error() string {
	return problem.Message
}
