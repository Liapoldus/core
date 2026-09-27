package models

type AdminMutationCommand struct {
	Request   CaddyAdminRequest
	Actor     string
	RequestID string
}
