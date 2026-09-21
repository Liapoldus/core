package models

type HeaderSet struct {
	Set         map[string]string
	SetIfAbsent map[string]string
	Delete      []string
}