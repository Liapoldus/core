package interfaces

import "context"

// PluginSecretReferenceResolver resolves one opaque configuration reference
// into bounded secret bytes only at redemption time.
type PluginSecretReferenceResolver func(context.Context, string, int64) ([]byte, error)
