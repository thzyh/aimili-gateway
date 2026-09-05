package webassets

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const testUIVersion = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestHandlerPrefersValidExternalRelease(t *testing.T) {
	root := makeExternalRelease(t, testUIVersion, "v1", map[string]string{
		"index.html":              `<html><body><div id="external"></div><script src="/assets/index-release.js"></script></body></html>`,
		"assets/index-release.js": "window.externalRelease = true",
	})
	response := httptest.NewRecorder()

	Handler(externalOptions(root, testUIVersion, "v1")).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="external"`) {
		t.Fatalf("external UI not served: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestHandlerFallsBackWhenExternalReleaseIsAPIIncompatible(t *testing.T) {
	root := makeExternalRelease(t, testUIVersion, "v2", map[string]string{
		"index.html":              `<html><body><div id="external"></div><script src="/assets/index-release.js"></script></body></html>`,
		"assets/index-release.js": "ok",
	})
	response := httptest.NewRecorder()

	Handler(externalOptions(root, testUIVersion, "v1")).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("embedded fallback missing: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestHandlerFallsBackWhenExternalIndexReferencesMissingAsset(t *testing.T) {
	root := makeExternalRelease(t, testUIVersion, "v1", map[string]string{
		"index.html": `<html><body><div id="external"></div><script src="/assets/missing.js"></script></body></html>`,
	})
	response := httptest.NewRecorder()

	Handler(externalOptions(root, testUIVersion, "v1")).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("release with missing entry asset was served: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestHandlerReturnsNotFoundForMissingStaticAsset(t *testing.T) {
	response := httptest.NewRecorder()

	Handler(Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))

	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("missing static asset did not return 404: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestHandlerServesExternalManifestWithoutCaching(t *testing.T) {
	root := makeExternalRelease(t, testUIVersion, "v1", map[string]string{"index.html": `<div id="external"></div>`})
	response := httptest.NewRecorder()
	Handler(externalOptions(root, testUIVersion, "v1")).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/manifest.json", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("manifest status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
}

func TestHandlerFallsBackToIndexOnlyForExtensionlessRoute(t *testing.T) {
	route := httptest.NewRecorder()
	Handler(Options{}).ServeHTTP(route, httptest.NewRequest(http.MethodGet, "/settings/updates", nil))
	if route.Code != http.StatusOK || !strings.Contains(route.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("SPA route did not receive index: status=%d", route.Code)
	}

	file := httptest.NewRecorder()
	Handler(Options{}).ServeHTTP(file, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if file.Code != http.StatusNotFound {
		t.Fatalf("missing file status=%d, want 404", file.Code)
	}
}

func TestOpenCurrentRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "index.html"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openCurrentWithResolver(root, "v1", func(string) (string, error) { return outside, nil }); err == nil {
		t.Fatal("escaping current symlink accepted")
	}
}

func TestOpenCurrentRejectsShortVersionID(t *testing.T) {
	root := makeExternalRelease(t, "aabb", "v1", map[string]string{"index.html": `<div id="short"></div>`})
	if _, _, err := openCurrentWithResolver(root, "v1", func(string) (string, error) {
		return filepath.Join(root, "releases", "aabb"), nil
	}); err == nil {
		t.Fatal("short release identifier accepted")
	}
}

func TestHandlerFallsBackWhenReleaseContainsUnlistedFile(t *testing.T) {
	root := makeExternalRelease(t, testUIVersion, "v1", map[string]string{
		"index.html":              `<div id="external"></div><script src="/assets/index-release.js"></script>`,
		"assets/index-release.js": "ok",
	})
	if err := os.WriteFile(filepath.Join(root, "releases", testUIVersion, "unexpected.txt"), []byte("unexpected"), 0o644); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	Handler(externalOptions(root, testUIVersion, "v1")).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("release with unlisted file was served: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestOpenCurrentRejectsManifestUnknownFields(t *testing.T) {
	root := makeExternalRelease(t, testUIVersion, "v1", map[string]string{"index.html": `<div id="external"></div>`})
	manifestPath := filepath.Join(root, "releases", testUIVersion, "manifest.json")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.TrimSuffix(string(body), "}") + `,"unexpected":true}`)
	if err := os.WriteFile(manifestPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openCurrentWithResolver(root, "v1", externalOptions(root, testUIVersion, "v1").resolvePath); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
}

func makeExternalRelease(t *testing.T, version, apiVersion string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	release := filepath.Join(root, "releases", version)
	for name, contents := range files {
		path := filepath.Join(release, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	type manifestFile struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	}
	manifestFiles := make([]manifestFile, 0, len(files))
	for name, contents := range files {
		digest := sha256.Sum256([]byte(contents))
		manifestFiles = append(manifestFiles, manifestFile{Path: name, SHA256: fmt.Sprintf("%x", digest), Bytes: int64(len(contents))})
	}
	sort.Slice(manifestFiles, func(i, j int) bool { return manifestFiles[i].Path < manifestFiles[j].Path })
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"kind":          "ui",
		"version":       version,
		"commit":        "abc1234",
		"builtAt":       "2026-09-05T00:00:00Z",
		"apiVersion":    apiVersion,
		"archive":       map[string]any{"sha256": version, "bytes": 1},
		"files":         manifestFiles,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func externalOptions(root, version, apiVersion string) Options {
	return Options{
		ExternalRoot: root,
		APIVersion:   apiVersion,
		resolvePath: func(string) (string, error) {
			return filepath.Join(root, "releases", version), nil
		},
	}
}
