package models

type GroupRevisionDetail struct {
	Revision  GroupRevision
	Caddyfile string
	Frontends []GroupRevisionFrontend
}
