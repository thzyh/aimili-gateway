package updatefetch

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func TestFetcherUsesOnlyConfiguredOriginAndExactAssetNames(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		paths = append(paths, request.URL.EscapedPath())
		mu.Unlock()
		_, _ = io.WriteString(response, "fixture")
	}))
	defer server.Close()
	fetcher := newTestFetcher(t, server)
	result, err := fetcher.Fetch(context.Background(), gatewayRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/stable/gateway/v1.2.3/manifest.json",
		"/stable/gateway/v1.2.3/manifest.sig",
		"/stable/gateway/v1.2.3/aimili-gateway",
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(paths, "\n") != strings.Join(want, "\n") || result.StagingDir == "" {
		t.Fatalf("paths=%q result=%#v", paths, result)
	}
}

func TestGitHubReleaseAssetsUseThePublicReleaseTagLayout(t *testing.T) {
	origin, err := url.Parse("https://github.com/thzyh/aimili-gateway/releases/download")
	if err != nil {
		t.Fatal(err)
	}
	actual := releaseAssetURL(origin, "stable", gatewayRequest(), "manifest.json")
	if actual.String() != "https://github.com/thzyh/aimili-gateway/releases/download/v1.2.3/manifest.json" {
		t.Fatalf("asset URL = %s", actual.String())
	}
}

func TestDownloadedRequestDoesNotDownloadTwice(t *testing.T) {
	hits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; _, _ = io.WriteString(w, "fixture") }))
	defer server.Close()
	fetcher := newTestFetcher(t, server)
	first, err := fetcher.Fetch(context.Background(), gatewayRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetcher.Fetch(context.Background(), gatewayRequest())
	if err != nil || first != second || hits != 3 {
		t.Fatalf("download was not idempotent: err=%v hits=%d", err, hits)
	}
}

func TestFetcherRejectsRedirectOutsideAllowlist(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	targetURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + "/stolen?signature=fixture"
	origin := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, targetURL, http.StatusFound)
	}))
	defer origin.Close()
	fetcher := newTestFetcher(t, origin)
	if _, err := fetcher.Fetch(context.Background(), gatewayRequest()); ErrorCode(err) != "blocked_redirect" {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestFetcherAllowsQueryOnAllowlistedHTTPSRedirect(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.RawQuery != "signature=fixture" {
			t.Fatalf("redirect query = %q", request.URL.RawQuery)
		}
		_, _ = io.WriteString(response, "fixture")
	}))
	defer target.Close()
	targetURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, targetURL+"/asset?signature=fixture", http.StatusFound)
	}))
	defer origin.Close()
	fetcher := newTestFetcher(t, origin)
	fetcher.Config.RedirectHosts = append(fetcher.Config.RedirectHosts, "localhost")
	client := origin.Client()
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	fetcher.Client = client
	if _, err := fetcher.Fetch(context.Background(), gatewayRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestFetcherRequiresHTTPSWithoutQueryOrCredentials(t *testing.T) {
	for name, origin := range map[string]string{
		"http":        "http://updates.example.test/releases",
		"query":       "https://updates.example.test/releases?token=secret",
		"credentials": "https://user:pass@updates.example.test/releases",
	} {
		t.Run(name, func(t *testing.T) {
			config := Config{ManifestOrigin: origin, RedirectHosts: []string{"updates.example.test"}, Channels: []string{"stable"}, PublicKeyFile: "fixture.pub", StagingRoot: t.TempDir(), MaxAssetBytes: 1024}
			if err := config.Validate(); err == nil {
				t.Fatalf("origin %q accepted", origin)
			}
		})
	}
}

func TestFetcherChecksFreeSpaceBeforeAnyRequest(t *testing.T) {
	hits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer server.Close()
	fetcher := newTestFetcher(t, server)
	fetcher.AvailableBytes = func(string) (uint64, error) { return 1, nil }
	if _, err := fetcher.Fetch(context.Background(), gatewayRequest()); ErrorCode(err) != "disk_full" {
		t.Fatalf("disk error = %v", err)
	}
	if hits != 0 {
		t.Fatalf("HTTP requests before disk gate = %d", hits)
	}
}

func TestFetcherNeverLogsCredentialOrCompleteURL(t *testing.T) {
	const credential = "Bearer secret-fixture"
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != credential {
			http.Error(response, "missing credential", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(response, "fixture")
	}))
	defer server.Close()
	fetcher := newTestFetcher(t, server)
	credentialFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credentialFile, []byte(credential+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fetcher.Config.CredentialFile = credentialFile
	var logs bytes.Buffer
	fetcher.Log = func(message string) { logs.WriteString(message) }
	if _, err := fetcher.Fetch(context.Background(), gatewayRequest()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), credential) || strings.Contains(logs.String(), server.URL+"/") {
		t.Fatalf("sensitive fetch log = %q", logs.String())
	}
}

func newTestFetcher(t *testing.T, server *httptest.Server) *Fetcher {
	t.Helper()
	host := strings.Split(strings.TrimPrefix(server.URL, "https://"), ":")[0]
	return &Fetcher{
		Config: Config{
			ManifestOrigin: server.URL,
			RedirectHosts:  []string{host},
			Channels:       []string{"stable"},
			PublicKeyFile:  "fixture.pub",
			StagingRoot:    t.TempDir(),
			MaxAssetBytes:  1024,
		},
		Client:         server.Client(),
		AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil },
		Verify:         func(updatetxn.Request, []byte, []byte, []byte) error { return nil },
	}
}

func gatewayRequest() updatetxn.Request {
	return updatetxn.Request{RunID: strings.Repeat("a", 64), Kind: updatetxn.KindGateway, Version: "v1.2.3", Action: updatetxn.ActionApply}
}

func TestErrorCodeUnwrapsFetcherErrors(t *testing.T) {
	err := &codedError{code: "fixture", err: errors.New("cause")}
	if ErrorCode(err) != "fixture" || !errors.Is(err, errors.Unwrap(err)) {
		t.Fatalf("coded error = %v", err)
	}
}

func TestReadPublicKeyAcceptsLowercaseHexKeygenFormat(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "release.pub")
	if err := os.WriteFile(filename, []byte(hex.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := readPublicKey(filename)
	if err != nil || !bytes.Equal(actual, publicKey) {
		t.Fatalf("key=%x err=%v", actual, err)
	}
}

func TestReadPublicKeyRejectsNonCanonicalKeygenFormat(t *testing.T) {
	publicKey := ed25519.PublicKey(bytes.Repeat([]byte{0xab}, ed25519.PublicKeySize))
	for name, body := range map[string]string{
		"uppercase hex":  strings.ToUpper(hex.EncodeToString(publicKey)),
		"mixed case hex": strings.Replace(hex.EncodeToString(publicKey), "ab", "aB", 1),
		"base64":         base64.StdEncoding.EncodeToString(publicKey),
	} {
		t.Run(name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "release.pub")
			if err := os.WriteFile(filename, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readPublicKey(filename); err == nil {
				t.Fatal("non-canonical public key accepted")
			}
		})
	}
}
