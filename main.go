// GileBrowser – a clean, configurable file download server.
package main

import (
	"embed"
	"log"

	"gileserver/config"
	"gileserver/server"
)

//go:embed templates static
var embeddedFS embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	server.SetStaticFS(embeddedFS)

	if err := server.Run(cfg, embeddedFS); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
