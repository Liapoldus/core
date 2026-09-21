package models

import "regexp"

type Rewrite struct {
	Pattern     *regexp.Regexp
	Replacement string
}