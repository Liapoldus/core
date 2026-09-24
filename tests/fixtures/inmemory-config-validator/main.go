package main

import (
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
)

func main() {
	if err := config.ValidateYAML(`state:
  path: ./data/gateway.db
artifacts:
  path: ./data/artifacts
management:
  listen: 127.0.0.1:9443
  tls:
    certificate: file:/run/secrets/management.crt
    key: file:/run/secrets/management.key
  bearerVerifier: file:/run/secrets/service-key-verifiers.json
caddy:
  variant: embedded
`); err != nil {
		os.Exit(1)
	}
}
