package models

type ASNMatcher struct {
	Provider string
	Config   DataProvider
	In       []uint
	NotIn    []uint
}
