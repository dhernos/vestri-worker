package http
import (
	"log"
	"net/http"
)

func Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)

	log.Printf("listening on %s \n", addr)
	return http.ListenAndServe(addr, mux)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}