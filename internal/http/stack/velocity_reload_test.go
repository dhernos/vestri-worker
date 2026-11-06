package stack

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVelocityReloadAcknowledgement(t *testing.T) {
	for _, output := range []string{"[INFO] VESTRI_DYNAMIC_SYNC_OK", "VESTRI_DYNAMIC_SYNC_OK"} {
		if !velocityReloadSucceeded(output) {
			t.Errorf("did not recognize %q", output)
		}
	}
	for _, output := range []string{"vestridynamic sync", "VESTRI_DYNAMIC_SYNC_FAILED", "The configuration has been reloaded", ""} {
		if velocityReloadSucceeded(output) {
			t.Errorf("accepted failed reload %q", output)
		}
	}
}

func TestVelocityReloadRejectsGet(t *testing.T) {
	response := httptest.NewRecorder()
	StackVelocityReloadHandler(response, httptest.NewRequest(http.MethodGet, "/stack/velocity/reload", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", response.Code)
	}
}
