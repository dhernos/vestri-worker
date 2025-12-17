package http

import (
	"net/http"
	"log"
)

func Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/settings", settingsHandler)
	mux.HandleFunc("/fs/read", readFileHandler)
	mux.HandleFunc("/fs/write", writeFileHandler)

	log.Printf("listening on %s \n", addr)
	return http.ListenAndServe(addr, mux)
}

