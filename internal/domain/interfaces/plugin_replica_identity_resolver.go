package interfaces

import "crypto/x509"

// PluginReplicaIdentityResolver maps a verified plugin replica client
// certificate to the plugin instance whose configuration generation it may pull.
// It lives in the domain interfaces layer because both the configuration pull
// endpoint and the CLI composition root that serves it must name this port.
type PluginReplicaIdentityResolver func(*x509.Certificate) (string, bool)
