package models

type GeoMatcher struct {
	Provider string
	Config   DataProvider
	Country  *StringMatcher
	City     *StringMatcher
}
