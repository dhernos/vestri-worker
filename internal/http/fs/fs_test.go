package fs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vestri-worker/internal/settings"
)

func TestDeleteHandlerRemovesCompleteMinecraftBase(t *testing.T) {
	base := t.TempDir()
	oldSettings := settings.Get()
	newSettings := oldSettings
	newSettings.FsBasePath = base
	settings.Set(newSettings)
	t.Cleanup(func() { settings.Set(oldSettings) })

	root := ".vestri-minecraft/705d3d41-0233-48c1-9904-b00fbb13389b/bases/9350e52f-a91e-427b-8c0c-c7688ffc5c4e"
	sibling := filepath.Join(base, filepath.Dir(root), "another-base", "data", "plugins")
	for _, directory := range []string{filepath.Join(base, root, "data", "plugins"), sibling} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := json.Marshal(map[string]interface{}{"path": root, "recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/fs/delete", strings.NewReader(string(payload)))
		response := httptest.NewRecorder()
		DeleteHandler(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("delete status = %d: %s", response.Code, response.Body.String())
		}
		if _, err := os.Stat(filepath.Join(base, root)); !os.IsNotExist(err) {
			t.Fatalf("base ID directory remains: %v", err)
		}
		if _, err := os.Stat(sibling); err != nil {
			t.Fatalf("sibling base was affected: %v", err)
		}
	}
}

func TestMinecraftResourceArchiveRoundTrip(t *testing.T) {
	for _, folder := range []bool{false, true} {
		name := "file"
		if folder {
			name = "folder"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			oldSettings := settings.Get()
			newSettings := oldSettings
			newSettings.FsBasePath = base
			settings.Set(newSettings)
			t.Cleanup(func() { settings.Set(oldSettings) })
			root := ".vestri-minecraft/node-a/bases/base-a/data"
			filePath := filepath.Join(root, "plugins", "Example", "config.yml")
			if err := os.MkdirAll(filepath.Join(base, filepath.Dir(filePath)), 0700); err != nil {
				t.Fatal(err)
			}
			content := "enabled: true\n"
			if err := os.WriteFile(filepath.Join(base, filePath), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			source := filePath
			extracted := filepath.Join(root, "extracted", "config.yml")
			if folder {
				source = filepath.Join(root, "plugins", "Example")
				extracted = filepath.Join(root, "extracted", "Example", "config.yml")
			}
			archive := filepath.Join(root, "download.zip")
			for _, action := range []struct {
				endpoint string
				handler  http.HandlerFunc
				source   string
				dest     string
			}{
				{"/fs/zip", ZipHandler, source, archive},
				{"/fs/unzip", UnzipHandler, archive, filepath.Join(root, "extracted")},
			} {
				payload, err := json.Marshal(map[string]string{"source": action.source, "dest": action.dest})
				if err != nil {
					t.Fatal(err)
				}
				response := httptest.NewRecorder()
				action.handler(response, httptest.NewRequest(http.MethodPost, action.endpoint, strings.NewReader(string(payload))))
				if response.Code != http.StatusOK {
					t.Fatalf("%s status = %d: %s", action.endpoint, response.Code, response.Body.String())
				}
			}
			data, err := os.ReadFile(filepath.Join(base, extracted))
			if err != nil || string(data) != content {
				t.Fatalf("extracted content = %q, error = %v", data, err)
			}
		})
	}
}

func TestReadFileHandlerLimitsRegularFiles(t *testing.T) {
	base := t.TempDir()
	oldSettings := settings.Get()
	newSettings := oldSettings
	newSettings.FsBasePath = base
	newSettings.MaxInlineReadBytes = 4
	settings.Set(newSettings)
	t.Cleanup(func() { settings.Set(oldSettings) })

	if err := os.WriteFile(filepath.Join(base, "small.txt"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "large.txt"), []byte("large"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "directory"), 0700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		path       string
		statusCode int
		body       string
	}{
		{name: "within limit", path: "small.txt", statusCode: http.StatusOK, body: "test"},
		{name: "over limit", path: "large.txt", statusCode: http.StatusRequestEntityTooLarge, body: "file too large\n"},
		{name: "directory", path: "directory", statusCode: http.StatusBadRequest, body: "path is not a regular file\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/fs/read?path="+tt.path, nil)
			recorder := httptest.NewRecorder()
			ReadFileHandler(recorder, req)
			if recorder.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.statusCode)
			}
			if got := recorder.Body.String(); got != tt.body {
				t.Fatalf("body = %q, want %q", got, tt.body)
			}
		})
	}
}
