package http

import (
	"log"
	"net/http"

	"vestri-worker/internal/http/fs"
	"vestri-worker/internal/http/stack"
)

func router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/fs/read", fs.ReadFileHandler)
	mux.HandleFunc("/fs/write", fs.WriteFileHandler)
	mux.HandleFunc("/fs/upload", fs.UploadFileHandler)
	mux.HandleFunc("/fs/download", fs.DownloadFileHandler)
	mux.HandleFunc("/fs/zip", fs.ZipHandler)
	mux.HandleFunc("/fs/unzip", fs.UnzipHandler)
	mux.HandleFunc("/stack/up", stack.StackUpHandler)
	mux.HandleFunc("/stack/down", stack.StackDownHandler)
	mux.HandleFunc("/stack/restart", stack.StackRestartHandler)
	mux.HandleFunc("/stack/status", stack.StackStatusHandler)
	return withMiddlewares(mux)
}

func Start(addr string) error {
	log.Printf("listening on %s (no TLS)\n", addr)
	return http.ListenAndServe(addr, router())
}

func StartTLS(addr, certFile, keyFile string) error {
	log.Printf("listening on %s (TLS)\n", addr)
	return http.ListenAndServeTLS(addr, certFile, keyFile, router())
}
