package models

type Site struct {
	Source        SourceKind
	Root          string
	Index         string
	SPA           bool
	Locales       []string
	DefaultLocale string
	Redirects     []SiteRedirect
	Headers       *HeaderActions
	Cache         *SiteCache
}
