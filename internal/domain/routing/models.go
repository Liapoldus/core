// Package routing defines transport-neutral matching and actions.
package routing

type Route struct{}
type Action struct{}

type Matcher interface {
	Matches(any) bool
}
