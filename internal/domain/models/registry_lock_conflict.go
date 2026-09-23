package models

type RegistryLockConflict struct{ Message string }

func NewRegistryLockConflict(message string) RegistryLockConflict {
	return RegistryLockConflict{Message: message}
}

func (conflict RegistryLockConflict) Error() string { return conflict.Message }
