package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunRejectsIncompleteUIInstallArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"ui-install", "--root", t.TempDir()}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "staging") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunRejectsIncompleteGatewayInstallArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"gateway-install", "--root", t.TempDir()}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "binary") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunSpoolRequiresConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"spool"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "config is required") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestGatewayHealthCheckReportsManifestVersionMismatchSafely(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"version":"other"}`))
	}))
	defer server.Close()
	err := gatewayHealthCheck(server.URL)(context.Background(), strings.Repeat("a", 64))
	if healthDetail(err) != "manifest_version_mismatch" || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("health error=%v detail=%q", err, healthDetail(err))
	}
}

func TestGatewayHealthCheckValidatesManifestIndexAndAssets(t *testing.T) {
	version := strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest.json":
			_, _ = response.Write([]byte(`{"version":"` + version + `"}`))
		case "/":
			_, _ = response.Write([]byte(`<script src="/assets/index.js"></script>`))
		case "/assets/index.js":
			_, _ = response.Write([]byte("ok"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	if err := gatewayHealthCheck(server.URL)(context.Background(), version); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsNonLoopbackHealthURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"ui-install", "--root", t.TempDir(), "--staging", t.TempDir(),
		"--public-key", "fixture.pub", "--health-url", "https://example.invalid",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "loopback") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
