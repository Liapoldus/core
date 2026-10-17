package interfaces

import "context"

// ServiceSecretReferenceResolver resolves one opaque configuration reference
// into bounded secret bytes only at redemption time.
type ServiceSecretReferenceResolver func(context.Context, string, int64) ([]byte, error)
