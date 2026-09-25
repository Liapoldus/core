package models

type ActiveServiceKeyExists struct {
	Message string
}

func (failure ActiveServiceKeyExists) Error() string {
	return failure.Message
}
