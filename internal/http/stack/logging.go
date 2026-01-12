package stack

import (
	"log"
	"net/http"
)

func logStackOp(r *http.Request, action, stack string) {
	log.Printf("stack %s %s action=%s stack=%q from=%s", r.Method, r.URL.Path, action, stack, r.RemoteAddr)
}

func logStackOpError(r *http.Request, action, stack string, err error) {
	if err == nil {
		log.Printf("stack %s %s action=%s stack=%q from=%s err=unknown", r.Method, r.URL.Path, action, stack, r.RemoteAddr)
		return
	}
	log.Printf("stack %s %s action=%s stack=%q from=%s err=%v", r.Method, r.URL.Path, action, stack, r.RemoteAddr, err)
}
