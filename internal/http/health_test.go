package http

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type healthTransport func(*http.Request) (*http.Response, error)

func (transport healthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestExternalConnectivity(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		github     int
		cloudflare int
		want       bool
		calls      int
	}{
		{"github available", 200, 503, true, 1},
		{"cloudflare fallback", 503, 200, true, 2},
		{"github unreachable", 0, 200, true, 2},
		{"both unavailable", 503, 503, false, 2},
		{"both unreachable", 0, 0, false, 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: healthTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodHead {
					t.Fatal("expected HEAD request")
				}
				code := scenario.github
				if request.URL.Host == "www.cloudflare.com" {
					code = scenario.cloudflare
				}
				if code == 0 {
					return nil, errors.New("unreachable")
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			if got := externalConnectivity(httptest.NewRequest(http.MethodGet, "/health", nil), client); got != scenario.want || calls != scenario.calls {
				t.Fatalf("connectivity=%v calls=%d; want %v calls=%d", got, calls, scenario.want, scenario.calls)
			}
		})
	}
}
