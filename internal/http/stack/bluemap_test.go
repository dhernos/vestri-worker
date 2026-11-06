package stack

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBlueMapPort(t *testing.T) {
	for _, tt := range []struct {
		name, config string
		port         int
	}{
		{"defaults", "# BlueMap\nwebroot: \"bluemap/web\"\n", 8100},
		{"generated", "enabled: true\nwebroot: \"bluemap/web\"\nport: 8100\nlog: {\n append: false\n}", 8100},
		{"custom", "enabled = true\nport = 8200 # internal only\n", 8200},
		{"last value wins", "enabled: false\nenabled: true\nport: 8100\nport: 8200\n", 8200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := blueMapPort(tt.config)
			if err != nil || got != tt.port {
				t.Fatalf("got port=%d, err=%v; want %d", got, err, tt.port)
			}
		})
	}
	for _, config := range []string{
		"enabled: false", "enabled: ${ENABLED}", "port: ${PORT}",
		"port: 0", "port: 65536", "port: -1", "port: not-a-port",
	} {
		if _, err := blueMapPort(config); err == nil {
			t.Errorf("accepted unavailable or invalid configuration: %q", config)
		}
	}
}

func TestBlueMapContainerIP(t *testing.T) {
	host, err := blueMapContainerIP([]byte(`[{"State":{"Running":true},"NetworkSettings":{"Networks":{"game":{"IPAddress":"172.18.0.2"}}}}]`))
	if err != nil || host != "172.18.0.2" {
		t.Fatalf("got %q, %v", host, err)
	}
	for _, data := range []string{
		`invalid`, `[]`, `[{"State":{"Running":false}}]`,
		`[{"State":{"Running":true},"NetworkSettings":{"Networks":{}}}]`,
		`[{"State":{"Running":true},"NetworkSettings":{"Networks":{"host":{"IPAddress":"127.0.0.1"}}}}]`,
	} {
		if _, err := blueMapContainerIP([]byte(data)); err == nil {
			t.Errorf("accepted unavailable Docker network: %s", data)
		}
	}
}

func TestValidBlueMapAsset(t *testing.T) {
	for _, asset := range []string{"", "index.html", "assets/app.js", "maps/world/tiles/0.json", "maps/My World/"} {
		if !validBlueMapAsset(asset) {
			t.Errorf("rejected asset %q", asset)
		}
	}
	for _, asset := range []string{"../secret", "a/../secret", "/secret", "a//b", `a\b`, "a\x00b"} {
		if validBlueMapAsset(asset) {
			t.Errorf("accepted invalid asset %q", asset)
		}
	}
}

func TestProxyBlueMapAssets(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(`{"tile":"world"}`))
	_ = writer.Close()
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/maps/My World/tile.json" || r.URL.RawQuery != "version=2" {
			t.Errorf("unexpected plugin URL: %s", r.URL)
		}
		for _, name := range []string{"Cookie", "Authorization", "X-API-Key", "X-Request-Timestamp", "X-Request-Nonce", "X-Request-Signature"} {
			if r.Header.Get(name) != "" {
				t.Errorf("credential %s reached the plugin", name)
			}
		}
		if r.Header.Get("Range") != "bytes=0-99" {
			t.Error("range request was lost")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Range", "bytes 0-99/100")
		w.Header().Set("Set-Cookie", "session_id=plugin; Path=/")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(compressed.Bytes())
	}))
	defer plugin.Close()
	target, _ := url.Parse(plugin.URL)
	req := httptest.NewRequest(http.MethodGet, "/stack/bluemap/view/maps/My%20World/tile.json?stack=private&root=private&version=2", nil)
	for _, name := range []string{"Cookie", "Authorization", "X-API-Key", "X-Request-Timestamp", "X-Request-Nonce", "X-Request-Signature"} {
		req.Header.Set(name, "secret")
	}
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Range", "bytes=0-99")
	rec := httptest.NewRecorder()
	proxyBlueMap(rec, req, target, "maps/My World/tile.json")
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), compressed.Bytes()) {
		t.Fatalf("compressed response changed: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Range") != "bytes 0-99/100" {
		t.Fatal("compression or range metadata lost")
	}
	if rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("map response could set panel cookies or enter a shared cache")
	}
}

func TestProxyBlueMapLiveUpdates(t *testing.T) {
	release := make(chan struct{})
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	defer plugin.Close()
	defer close(release)
	target, _ := url.Parse(plugin.URL)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyBlueMap(w, r, target, "maps/world/live/updates")
	}))
	defer proxy.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("live update was buffered: %q, %v", line, err)
	}
}

func TestProxyBlueMapErrorsAndRedirects(t *testing.T) {
	for _, tt := range []struct {
		location string
		status   int
	}{
		{"/maps/world/?v=2", http.StatusFound},
		{"https://other.example/map", http.StatusBadGateway},
	} {
		t.Run(tt.location, func(t *testing.T) {
			plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", tt.location)
				w.WriteHeader(http.StatusFound)
			}))
			defer plugin.Close()
			target, _ := url.Parse(plugin.URL)
			rec := httptest.NewRecorder()
			proxyBlueMap(rec, httptest.NewRequest(http.MethodGet, "/", nil), target, "")
			if rec.Code != tt.status {
				t.Fatalf("got %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
		})
	}
	plugin := httptest.NewServer(http.NotFoundHandler())
	target, _ := url.Parse(plugin.URL)
	plugin.Close()
	rec := httptest.NewRecorder()
	proxyBlueMap(rec, httptest.NewRequest(http.MethodGet, "/", nil), target, "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "not reachable") {
		t.Fatalf("unreachable BlueMap was not surfaced: %d %s", rec.Code, rec.Body)
	}
}
