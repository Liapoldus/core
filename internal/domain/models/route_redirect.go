package models

type RouteRedirect struct {
	Scheme        string
	Host          string
	Path          string
	PreserveQuery bool
	Status        int
}