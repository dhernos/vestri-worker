package fs

import (
	"log"
	"net/http"
)

func logPathOp(r *http.Request, action, path string) {
	log.Printf("fs %s %s action=%s path=%q from=%s", r.Method, r.URL.Path, action, path, r.RemoteAddr)
}

func logPathOpError(r *http.Request, action, path string, err error) {
	if err == nil {
		log.Printf("fs %s %s action=%s path=%q from=%s err=unknown", r.Method, r.URL.Path, action, path, r.RemoteAddr)
		return
	}
	log.Printf("fs %s %s action=%s path=%q from=%s err=%v", r.Method, r.URL.Path, action, path, r.RemoteAddr, err)
}

func logArchiveOp(r *http.Request, action, source, dest string) {
	log.Printf("fs %s %s action=%s source=%q dest=%q from=%s", r.Method, r.URL.Path, action, source, dest, r.RemoteAddr)
}

func logArchiveOpError(r *http.Request, action, source, dest string, err error) {
	if err == nil {
		log.Printf("fs %s %s action=%s source=%q dest=%q from=%s err=unknown", r.Method, r.URL.Path, action, source, dest, r.RemoteAddr)
		return
	}
	log.Printf("fs %s %s action=%s source=%q dest=%q from=%s err=%v", r.Method, r.URL.Path, action, source, dest, r.RemoteAddr, err)
}
