package main

import (
	"fmt"
	"os"

	_ "github.com/Liapoldus/core/internal/infrastructure/caddy"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	_ "github.com/mholt/caddy-l4/layer4"
)

const buildIdentity = "liapoldus-external-caddy-fixture"

func main() {
	for _, argument := range os.Args[1:] {
		if argument == "version" {
			fmt.Println(buildIdentity)
			return
		}
	}
	caddycmd.Main()
}
