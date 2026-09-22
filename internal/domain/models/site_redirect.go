package models

type SiteRedirect struct {
	From           string
	To             string
	Status         int
	LocationHeader string
}
