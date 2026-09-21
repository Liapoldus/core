package models

type PreparedSnapshot struct {
	snapshot Snapshot
}

func NewPreparedSnapshot(snapshot Snapshot) PreparedSnapshot {
	return PreparedSnapshot{snapshot: snapshot}
}

func (prepared PreparedSnapshot) Snapshot() Snapshot {
	return prepared.snapshot
}
