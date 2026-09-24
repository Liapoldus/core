package caddy

import (
	caddycore "github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	_ "github.com/mholt/caddy-l4"
)

func RegisteredModules() []string {
	return caddycore.Modules()
}
