// Package registry defines immutable site release storage.
package registry

type Release struct {
	ID string
}

type Store interface {
	Publish(site, source string) (Release, error)
	Rollback(site string) (Release, error)
}
