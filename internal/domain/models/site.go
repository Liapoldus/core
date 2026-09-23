package models

type Site struct {
	Source          SourceKind
	Root            string
	Index           string
	SPA             bool
	Locales         []string
	DefaultLocale   string
	LocaleSeparator string
	Redirects       []SiteRedirect
	Headers         *HeaderActions
	Cache           *SiteCache
}
