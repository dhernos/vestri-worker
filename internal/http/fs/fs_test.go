package fs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"vestri-worker/internal/settings"
)

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
