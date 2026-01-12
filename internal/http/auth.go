package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"vestri-worker/internal/settings"
)

const (
	headerAPIKey     = "X-API-Key"
	headerTimestamp  = "X-Request-Timestamp"
	headerNonce      = "X-Request-Nonce"
	headerSignature  = "X-Request-Signature"
	defaultReplayTTL = 300
	maxNonceLength   = 128
	maxNonceEntries  = 200000
)

var nonceCache nonceStore
var authFailLimiter rateLimiter

type authContextKey struct{}

var authKeyContextKey = &authContextKey{}

type nonceStore struct {
	mu          sync.Mutex
	entries     map[string]time.Time
	lastCleanup time.Time
}

func authMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := settings.Get()
			if r.URL.Path == "/health" && !cfg.HealthRequiresAuth {
				next.ServeHTTP(w, r)
				return
			}
			apiKeyConfig := settings.GetAPIKey()
			if apiKeyConfig == "" {
				next.ServeHTTP(w, r)
				return
			}

			apiKey := r.Header.Get(headerAPIKey)
			if !secureEqual(apiKey, apiKeyConfig) {
				rejectUnauthorized(w, r, cfg)
				return
			}

			timestamp := strings.TrimSpace(r.Header.Get(headerTimestamp))
			nonce := strings.TrimSpace(r.Header.Get(headerNonce))
			signature := strings.TrimSpace(r.Header.Get(headerSignature))
			if timestamp == "" || nonce == "" || signature == "" {
				rejectUnauthorized(w, r, cfg)
				return
			}
			if len(nonce) > maxNonceLength {
				rejectUnauthorized(w, r, cfg)
				return
			}

			ts, err := strconv.ParseInt(timestamp, 10, 64)
			if err != nil {
				rejectUnauthorized(w, r, cfg)
				return
			}

			replayTTL := cfg.ReplayWindowSeconds
			if replayTTL <= 0 {
				replayTTL = defaultReplayTTL
			}

			allowedSkew := time.Duration(replayTTL) * time.Second
			now := time.Now()
			if delta := now.Sub(time.Unix(ts, 0)); delta > allowedSkew || delta < -allowedSkew {
				rejectUnauthorized(w, r, cfg)
				return
			}

			expected := buildSignature(apiKeyConfig, timestamp, nonce, r.Method, r.URL.RequestURI())
			if !secureEqual(signature, expected) {
				rejectUnauthorized(w, r, cfg)
				return
			}

			if !nonceCache.use(nonce, allowedSkew) {
				rejectUnauthorized(w, r, cfg)
				return
			}

			ctx := withAuthKey(r.Context(), apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func buildSignature(secret, timestamp, nonce, method, uri string) string {
	payload := strings.Join([]string{timestamp, nonce, method, uri}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func withAuthKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, authKeyContextKey, key)
}

func authKeyFromContext(ctx context.Context) (string, bool) {
	value := ctx.Value(authKeyContextKey)
	if value == nil {
		return "", false
	}
	key, ok := value.(string)
	return key, ok
}

func rejectUnauthorized(w http.ResponseWriter, r *http.Request, cfg settings.Settings) {
	if !authFailLimiter.allow(clientKey(r), cfg.RateLimitRPS, cfg.RateLimitBurst) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (s *nonceStore) use(nonce string, ttl time.Duration) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.entries == nil {
		s.entries = make(map[string]time.Time)
	}

	if exp, ok := s.entries[nonce]; ok && exp.After(now) {
		return false
	}

	s.entries[nonce] = now.Add(ttl)
	s.cleanupIfNeeded(now)

	return true
}

func (s *nonceStore) cleanupIfNeeded(now time.Time) {
	if now.Sub(s.lastCleanup) < time.Minute {
		return
	}
	for key, exp := range s.entries {
		if exp.Before(now) {
			delete(s.entries, key)
		}
	}
	s.lastCleanup = now

	if len(s.entries) > maxNonceEntries {
		for key := range s.entries {
			delete(s.entries, key)
		}
	}
}
