package models

type OperationReservation struct {
	Operation     Operation
	Scope         string
	Key           string
	RequestDigest string
	Payload       *OperationPayload
}
