package main

import(
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

	cfg := settings.Get()

	if err := http.Start(cfg.HTTPPort); err != nil {
		log.Fatal(err)
	}
}
