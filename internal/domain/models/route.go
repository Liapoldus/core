package models

type Route struct {
	When PathMatcher
	Site string
}