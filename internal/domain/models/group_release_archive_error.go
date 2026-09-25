package models

type GroupReleaseArchiveError struct {
	Cause    error
	TooLarge bool
}

func (archive GroupReleaseArchiveError) Error() string {
	if archive.Cause == nil {
		return ""
	}
	return archive.Cause.Error()
}

func (archive GroupReleaseArchiveError) Unwrap() error {
	return archive.Cause
}
