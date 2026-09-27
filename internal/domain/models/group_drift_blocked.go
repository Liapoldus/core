package models

type GroupDriftBlocked struct {
	Message string
}

func (problem GroupDriftBlocked) Error() string {
	return problem.Message
}
