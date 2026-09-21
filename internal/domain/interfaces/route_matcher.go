package interfaces

type RouteMatcher interface {
	Matches(any) bool
}
