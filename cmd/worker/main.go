package main

import (
	"log"

	"vestri-worker/internal/http"
	"vestri-worker/internal/settings"
)

func main() {
	log.Println("starting worker")

	settingsPath := "/etc/vestri/settings.json"

	if err := settings.Load(settingsPath); err != nil {
		log.Fatal(err)
	}

	key, generated, err := settings.EnsureAPIKey()
	if err != nil {
		log.Fatal(err)
	}
	if generated {
		log.Printf("generated API key: %s", key)
	}

	cfg := settings.Get()
	if settings.GetAPIKey() != "" && !cfg.UseTLS && !cfg.TrustProxyHeaders && !cfg.RequireTLS {
		log.Println("warning: API key is sent over plaintext HTTP without TLS")
	}
	if cfg.RequireTLS && !cfg.UseTLS && !cfg.TrustProxyHeaders {
		log.Println("warning: require_tls is enabled but TLS/proxy headers are disabled; requests will be rejected")
	}

	if cfg.UseTLS {
		log.Println("starting HTTP server with TLS enabled")
		if err := http.StartTLS(cfg.HTTPPort, cfg.TLSCert, cfg.TLSKey); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Println("starting HTTP server without TLS")
		if err := http.Start(cfg.HTTPPort); err != nil {
			log.Fatal(err)
		}
	}
}
