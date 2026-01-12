package http

import (
	"net/http"
	"strings"

	"vestri-worker/internal/settings"
)

const hstsHeaderValue = "max-age=31536000"

func tlsMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := settings.Get()
			secure := isSecureRequest(r, cfg.TrustProxyHeaders)

			if secure {
				w.Header().Set("Strict-Transport-Security", hstsHeaderValue)
			} else if cfg.RequireTLS {
				http.Error(w, "TLS required", http.StatusUpgradeRequired)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isSecureRequest(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	if !trustProxy {
		return false
	}

	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto != "" {
		if strings.EqualFold(strings.SplitN(proto, ",", 2)[0], "https") {
			return true
		}
	}

	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Ssl")), "on") {
		return true
	}

	return false
}
