package models

type TrafficRolloutSubmission struct {
	Reservation      OperationReservation
	ExpectedRevision int64
	SchemaVersion    int64
	SettingsJSON     []byte
	Record           TrafficRolloutRecord
	Audit            AuditRecord
}
