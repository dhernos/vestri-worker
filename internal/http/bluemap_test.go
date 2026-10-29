package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"vestri-worker/internal/settings"
)

func TestBlueMapRateLimitSeparateBudget(t *testing.T) {
	previous := settings.Get()
	cfg := settings.Default()
	cfg.RateLimitRPS = 0.000001
	cfg.RateLimitBurst = 2
	settings.Set(cfg)
	defer settings.Set(previous)
	limiter = rateLimiter{}
	defer func() { limiter = rateLimiter{} }()
	handler := rateLimitMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 41; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stack/bluemap/view/maps/world/tile.json", nil))
		want := http.StatusOK
		if i == 40 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("map asset %d returned %d, want %d", i, rec.Code, want)
		}
	}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stack/status", nil))
		want := http.StatusOK
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("control request %d returned %d, want %d", i, rec.Code, want)
		}
	}
}
