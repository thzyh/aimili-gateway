//go:build linux

package webassets

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerFollowsLinuxCurrentSymlink(t *testing.T) {
	root := makeExternalRelease(t, testUIVersion, "v1", map[string]string{
		"index.html":             `<div id="linux-external"></div><script src="/assets/index-release.js"></script>`,
		"assets/index-release.js": "ok",
	})
	if err := os.Symlink(filepath.Join("releases", testUIVersion), filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	Handler(Options{ExternalRoot: root, APIVersion: "v1"}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="linux-external"`) {
		t.Fatalf("Linux current release not served: status=%d body=%q", response.Code, response.Body.String())
	}
}
