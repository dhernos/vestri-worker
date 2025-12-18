package http

import (
	"log"
	"net/http"
)

func router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/settings", settingsHandler)
	mux.HandleFunc("/fs/read", readFileHandler)
	mux.HandleFunc("/fs/write", writeFileHandler)

	return mux
}

func Start(addr string) error {
	log.Printf("listening on %s (no TLS)\n", addr)
	return http.ListenAndServe(addr, router())
}

func StartTLS(addr, certFile, keyFile string) error {
	log.Printf("listening on %s (TLS)\n", addr)
	return http.ListenAndServeTLS(addr, certFile, keyFile, router())
}
