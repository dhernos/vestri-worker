package http

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"vestri-worker/internal/settings"
)

const rateLimitTTL = 10 * time.Minute

var limiter rateLimiter

type rateLimiter struct {
	mu          sync.Mutex
	clients     map[string]*rateClient
	lastCleanup time.Time
}

type rateClient struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

func rateLimitMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := settings.Get()
			if !limiter.allow(rateLimitKey(r), cfg.RateLimitRPS, cfg.RateLimitBurst) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientKey(r *http.Request) string {
	cfg := settings.Get()
	if cfg.TrustProxyHeaders {
		if forwarded := firstForwardedFor(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func firstForwardedFor(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, ",")
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func rateLimitKey(r *http.Request) string {
	if key, ok := authKeyFromContext(r.Context()); ok && key != "" {
		sum := sha256.Sum256([]byte(key))
		return "key:" + hex.EncodeToString(sum[:])
	}
	return "ip:" + clientKey(r)
}

func (l *rateLimiter) allow(key string, rate float64, burst int) bool {
	if rate <= 0 || burst <= 0 {
		return true
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.clients == nil {
		l.clients = make(map[string]*rateClient)
	}

	client := l.clients[key]
	if client == nil {
		client = &rateClient{
			tokens:   float64(burst),
			last:     now,
			lastSeen: now,
		}
		l.clients[key] = client
	}

	elapsed := now.Sub(client.last).Seconds()
	client.tokens += elapsed * rate
	if client.tokens > float64(burst) {
		client.tokens = float64(burst)
	}
	client.last = now
	client.lastSeen = now

	if client.tokens < 1 {
		l.cleanupIfNeeded(now)
		return false
	}
	client.tokens -= 1

	l.cleanupIfNeeded(now)
	return true
}

func (l *rateLimiter) cleanupIfNeeded(now time.Time) {
	if now.Sub(l.lastCleanup) < time.Minute {
		return
	}

	cutoff := now.Add(-rateLimitTTL)
	for key, client := range l.clients {
		if client.lastSeen.Before(cutoff) {
			delete(l.clients, key)
		}
	}

	l.lastCleanup = now
}
