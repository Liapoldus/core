package models

import "time"

type AuditRecord struct {
	Timestamp    time.Time `json:"timestamp"`
	Actor        string    `json:"actor"`
	Action       string    `json:"action"`
	Resource     string    `json:"resource"`
	Result       string    `json:"result"`
	RequestID    string    `json:"requestId"`
	DigestBefore string    `json:"digestBefore,omitempty"`
	DigestAfter  string    `json:"digestAfter,omitempty"`
}
