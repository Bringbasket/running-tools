package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesGeneratedViteAssets(t *testing.T) {
	handler, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	assets, err := fs.Glob(handler.files, "assets/_*.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) == 0 {
		assets, err = fs.Glob(handler.files, "assets/*.js")
	}
	if err != nil || len(assets) == 0 {
		t.Fatalf("generated Vite asset not found: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/"+assets[0], nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if !strings.Contains(response.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("asset returned wrong content type: %s", response.Header().Get("Content-Type"))
	}
	if strings.Contains(response.Body.String(), "<!doctype html>") {
		t.Fatal("asset request fell back to the SPA index")
	}
}
