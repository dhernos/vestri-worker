package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"vestri-worker/internal/http/fs"
	"vestri-worker/internal/settings"
)

const blueMapViewPrefix = "/stack/bluemap/view/"

var blueMapTargets = struct {
	sync.Mutex
	entries map[string]blueMapTarget
}{entries: make(map[string]blueMapTarget)}

type blueMapTarget struct {
	url     *url.URL
	expires time.Time
}

// BlueMap's generated HOCON uses literal top-level values for these settings.
var blueMapSetting = regexp.MustCompile(`^\s*(enabled|port)\s*[:=]\s*([^#]+?)(?:\s*#.*)?$`)

func blueMapPort(config string) (int, error) {
	port := 8100
	enabled := true
	for _, line := range strings.Split(config, "\n") {
		match := blueMapSetting.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(match[2]), ","))
		switch match[1] {
		case "enabled":
			if value != "true" && value != "false" {
				return 0, fmt.Errorf("BlueMap enabled must be a literal boolean")
			}
			enabled = value == "true"
		case "port":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 65535 {
				return 0, fmt.Errorf("BlueMap port must be a literal number between 1 and 65535")
			}
			port = n
		}
	}
	if !enabled {
		return 0, fmt.Errorf("BlueMap's integrated webserver is disabled")
	}
	return port, nil
}

func resolveBlueMapTarget(ctx context.Context, stackPath, root string) (*url.URL, error) {
	key := stackPath + "\n" + root
	blueMapTargets.Lock()
	defer blueMapTargets.Unlock()
	if cached, ok := blueMapTargets.entries[key]; ok && time.Now().Before(cached.expires) {
		return cached.url, nil
	}

	base := settings.Get().FsBasePath
	configPath, err := fs.SafePath(base, filepath.Join(root, "data", "plugins", "BlueMap", "webserver.conf"))
	if err != nil {
		return nil, err
	}
	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("BlueMap configuration is unavailable; install BlueMap and start the server once")
		}
		return nil, fmt.Errorf("failed to open BlueMap configuration: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect BlueMap configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("BlueMap configuration must be a regular file smaller than 1 MiB")
	}
	config, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read BlueMap configuration: %w", err)
	}
	if len(config) > 1<<20 {
		return nil, fmt.Errorf("BlueMap configuration exceeds 1 MiB")
	}
	port, err := blueMapPort(string(config))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	service, err := resolveDefaultComposeService(ctx, stackPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the Minecraft service: %w", err)
	}
	cmd, err := composeCommandContext(ctx, stackPath, "ps", "-q", service)
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to find the running Minecraft container: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) != 1 {
		return nil, fmt.Errorf("start the Minecraft server before opening the map")
	}
	out, err = exec.CommandContext(ctx, "docker", "inspect", ids[0]).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect the Minecraft container: %w", err)
	}
	host, err := blueMapContainerIP(out)
	if err != nil {
		return nil, err
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port))}
	now := time.Now()
	for key, entry := range blueMapTargets.entries {
		if now.After(entry.expires) {
			delete(blueMapTargets.entries, key)
		}
	}
	blueMapTargets.entries[key] = blueMapTarget{url: target, expires: now.Add(10 * time.Second)}
	return target, nil
}

func blueMapContainerIP(data []byte) (string, error) {
	var containers []struct {
		State           struct{ Running bool }
		NetworkSettings struct {
			Networks map[string]struct{ IPAddress string }
		}
	}
	if err := json.Unmarshal(data, &containers); err != nil {
		return "", fmt.Errorf("invalid Docker inspect response: %w", err)
	}
	if len(containers) != 1 || !containers[0].State.Running {
		return "", fmt.Errorf("the Minecraft container is not running")
	}
	for _, network := range containers[0].NetworkSettings.Networks {
		if ip := net.ParseIP(network.IPAddress); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("the Minecraft container needs a Docker bridge network reachable by the worker")
}

func BlueMapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stackPath, err := parseStackName(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, err := resolveBlueMapTarget(r.Context(), stackPath, r.URL.Query().Get("root"))
	if err != nil {
		logStackOpError(r, "bluemap", filepath.Base(stackPath), err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	asset := strings.TrimPrefix(r.URL.Path, blueMapViewPrefix)
	if r.URL.Path == "/stack/bluemap/status" {
		asset = ""
	} else if !strings.HasPrefix(r.URL.Path, blueMapViewPrefix) {
		http.NotFound(w, r)
		return
	}
	if !validBlueMapAsset(asset) {
		http.Error(w, "invalid map path", http.StatusBadRequest)
		return
	}
	proxyBlueMap(w, r, target, asset)
}

func validBlueMapAsset(asset string) bool {
	return !strings.ContainsAny(asset, "\\\x00") &&
		!strings.Contains(asset, "//") &&
		path.Clean("/"+asset) == "/"+strings.TrimSuffix(asset, "/")
}

func proxyBlueMap(w http.ResponseWriter, r *http.Request, target *url.URL, asset string) {
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := &http.Transport{
		Proxy: nil, ResponseHeaderTimeout: 15 * time.Second,
		DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	}
	proxy.Transport = transport
	defer transport.CloseIdleConnections()
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.URL.Path = "/" + asset
		req.URL.RawPath = ""
		query := req.URL.Query()
		query.Del("stack")
		query.Del("root")
		req.URL.RawQuery = query.Encode()
		req.Host = target.Host
		// Neither panel sessions nor worker credentials belong at the plugin.
		for _, name := range []string{"Cookie", "Authorization", "X-API-Key", "X-Request-Timestamp", "X-Request-Nonce", "X-Request-Signature"} {
			req.Header.Del(name)
		}
	}
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		if location := resp.Header.Get("Location"); location != "" {
			redirect, err := url.Parse(location)
			if err != nil || (redirect.Host != "" && redirect.Host != target.Host) {
				return fmt.Errorf("BlueMap returned an external redirect")
			}
			redirect.Scheme, redirect.Host = "", ""
			resp.Header.Set("Location", redirect.String())
		}
		resp.Header.Set("Cache-Control", "private, no-store")
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Header.Set("X-Accel-Buffering", "no")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("bluemap proxy failed: %v", err)
		http.Error(w, "BlueMap is not reachable; check that its webserver is enabled and the Minecraft server is running", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
