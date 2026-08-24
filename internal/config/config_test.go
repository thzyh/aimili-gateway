package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesSafeLocalDefaults(t *testing.T) {
	clearConfigEnvironment(t)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddress != "127.0.0.1:9080" {
		t.Fatalf("listen address = %q", cfg.ListenAddress)
	}
	if cfg.DatabasePath != filepath.FromSlash("data/aimili-gateway.db") {
		t.Fatalf("database path = %q", cfg.DatabasePath)
	}
	if cfg.MasterKeyFile != filepath.FromSlash("data/master.key") {
		t.Fatalf("master key file = %q", cfg.MasterKeyFile)
	}
	if cfg.AimiliAddress != "127.0.0.1:8787" {
		t.Fatalf("Aimili address = %q", cfg.AimiliAddress)
	}
	if cfg.XUIBaseURL != "http://127.0.0.1:2001/" {
		t.Fatalf("3x-ui base URL = %q", cfg.XUIBaseURL)
	}
}

func TestValidateRejectsPublicListenAddress(t *testing.T) {
	cfg := validProductionConfig()
	cfg.ListenAddress = "0.0.0.0:9080"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback validation error, got %v", err)
	}
}

func TestValidateRequiresExactHTTPSPublicOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
	}{
		{name: "missing", origin: ""},
		{name: "plain HTTP", origin: "http://console.example.test"},
		{name: "path", origin: "https://console.example.test/admin"},
		{name: "query", origin: "https://console.example.test?debug=1"},
		{name: "credentials", origin: "https://user@console.example.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validProductionConfig()
			cfg.PublicOrigin = tt.origin
			if err := cfg.Validate(); err == nil {
				t.Fatalf("origin %q was accepted", tt.origin)
			}
		})
	}
}

func TestLoadRejectsUnknownJSONFields(t *testing.T) {
	clearConfigEnvironment(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{
		"listenAddress":"127.0.0.1:9080",
		"publicOrigin":"https://console.example.test",
		"databasePath":"gateway.db",
		"masterKeyFile":"master.key",
		"aimiliAddress":"127.0.0.1:8787",
		"xuiBaseUrl":"http://127.0.0.1:2001/",
		"expertModeUrl":"/expert/",
		"unexpected":true
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestLoadAppliesDocumentedEnvironmentOverrides(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("GATEWAY_LISTEN_ADDRESS", "[::1]:9191")
	t.Setenv("GATEWAY_DATABASE_PATH", filepath.FromSlash("custom/gateway.db"))
	t.Setenv("GATEWAY_AIMILI_ADDRESS", "[::1]:8787")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "[::1]:9191" {
		t.Fatalf("listen address = %q", cfg.ListenAddress)
	}
	if cfg.DatabasePath != filepath.FromSlash("custom/gateway.db") {
		t.Fatalf("database path = %q", cfg.DatabasePath)
	}
	if cfg.AimiliAddress != "[::1]:8787" {
		t.Fatalf("Aimili address = %q", cfg.AimiliAddress)
	}
}

func validProductionConfig() Config {
	return Config{
		ListenAddress: "127.0.0.1:9080",
		PublicOrigin:  "https://console.example.test",
		DatabasePath:  filepath.FromSlash("data/gateway.db"),
		MasterKeyFile: filepath.FromSlash("data/master.key"),
		AimiliAddress: "127.0.0.1:8787",
		XUIBaseURL:    "http://127.0.0.1:2001/",
		ExpertModeURL: "/expert/",
	}
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
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
}
