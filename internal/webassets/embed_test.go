package webassets

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexWithoutCaching(t *testing.T) {
	response := httptest.NewRecorder()
	Handler(Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatal("index page missing application root")
	}
}

func TestHandlerCachesHashedAssetsImmutably(t *testing.T) {
	asset := firstHashedAsset(t)
	if asset == "" {
		t.Fatal("hashed asset missing")
	}
	response := httptest.NewRecorder()
	Handler(Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+asset, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
}

func firstHashedAsset(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(distribution, "assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "index-") {
			return "assets/" + entry.Name()
		}
	}
	return ""
}

func TestHandlerFallsBackToIndexForSPARoute(t *testing.T) {
	response := httptest.NewRecorder()
	Handler(Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatal("SPA route did not receive index page")
	}
}
