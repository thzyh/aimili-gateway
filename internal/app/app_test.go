package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/config"
)

func TestNewProvidesHealthHandlerAndClosesIdempotently(t *testing.T) {
	directory := t.TempDir()
	masterKeyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(masterKeyPath, []byte(strings.Repeat("k", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.Context(), config.Config{
		ListenAddress: "127.0.0.1:9080",
		PublicOrigin:  "https://console.example.test",
		DatabasePath:  filepath.Join(directory, "gateway.db"),
		MasterKeyFile: masterKeyPath,
		AimiliAddress: "127.0.0.1:8787",
		XUIBaseURL:    "http://127.0.0.1:2001/panel-fixture/",
		ExpertModeURL: "/expert-fixture/",
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
	if strings.TrimSpace(response.Body.String()) != `{"status":"ok"}` {
		t.Fatal("health response mismatch")
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsInvalidMasterKeyLength(t *testing.T) {
	directory := t.TempDir()
	masterKeyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(masterKeyPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(t.Context(), config.Config{
		ListenAddress: "127.0.0.1:9080",
		PublicOrigin:  "https://console.example.test",
		DatabasePath:  filepath.Join(directory, "gateway.db"),
		MasterKeyFile: masterKeyPath,
		AimiliAddress: "127.0.0.1:8787",
		XUIBaseURL:    "http://127.0.0.1:2001/panel-fixture/",
	})
	if err == nil {
		t.Fatal("invalid master key accepted")
	}
}

func TestNewSupportsExplicitLocalTestConfiguration(t *testing.T) {
	for _, name := range []string{
		"GATEWAY_LISTEN_ADDRESS",
		"GATEWAY_PUBLIC_ORIGIN",
		"GATEWAY_DATABASE_PATH",
		"GATEWAY_MASTER_KEY_FILE",
		"GATEWAY_AIMILI_ADDRESS",
		"GATEWAY_XUI_BASE_URL",
		"GATEWAY_EXPERT_MODE_URL",
	} {
		t.Setenv(name, "")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	cfg.DatabasePath = filepath.Join(directory, "gateway.db")
	cfg.MasterKeyFile = filepath.Join(directory, "master.key")
	if err := os.WriteFile(cfg.MasterKeyFile, []byte(strings.Repeat("k", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
}
