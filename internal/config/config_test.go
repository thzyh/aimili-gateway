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
	if cfg.AimiliControlURL != "http://127.0.0.1:8790/" {
		t.Fatalf("Aimili control URL = %q", cfg.AimiliControlURL)
	}
	if cfg.AimiliControlTokenFile != filepath.FromSlash("data/aimili-control.token") {
		t.Fatalf("Aimili control token file = %q", cfg.AimiliControlTokenFile)
	}
	if cfg.XUIBaseURL != "http://127.0.0.1:2001/" {
		t.Fatalf("3x-ui base URL = %q", cfg.XUIBaseURL)
	}
	if cfg.XUICredentialsFile != filepath.FromSlash("data/xui-automation.json") {
		t.Fatalf("3x-ui credentials file = %q", cfg.XUICredentialsFile)
	}
	if cfg.MaxProxyGroups != 1 || cfg.VLESSPortStart != 20000 || cfg.VLESSPortEnd != 20999 ||
		cfg.MixedPortStart != 30000 || cfg.MixedPortEnd != 30999 || cfg.ProbeHost != "api.ipify.org" || cfg.XrayPath == "" {
		t.Fatalf("proxy runtime defaults are incomplete: %#v", cfg)
	}
	if cfg.ProtocolRequestDir != filepath.FromSlash("data/protocol-spool/requests") || cfg.ProtocolResultDir != filepath.FromSlash("data/protocol-spool/results") || cfg.ProtocolTimeoutSeconds != 180 {
		t.Fatalf("protocol transaction defaults are incomplete: %#v", cfg)
	}
}

func TestValidateRequiresSiblingProtocolSpoolDirectories(t *testing.T) {
	cfg := validProductionConfig()
	for _, mutate := range []func(*Config){
		func(value *Config) { value.ProtocolRequestDir = "relative/requests" },
		func(value *Config) { value.ProtocolResultDir = filepath.FromSlash("/var/lib/other/results") },
		func(value *Config) { value.ProtocolResultDir = value.ProtocolRequestDir },
		func(value *Config) { value.ProtocolTimeoutSeconds = 0 },
		func(value *Config) { value.ProtocolTimeoutSeconds = 601 },
	} {
		candidate := cfg
		mutate(&candidate)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("unsafe protocol spool accepted: %#v", candidate)
		}
	}
}

func TestValidateAcceptsAbsoluteExternalUIRoot(t *testing.T) {
	cfg := validProductionConfig()
	cfg.ExternalUIRoot = filepath.FromSlash("/var/lib/aimili-gateway/ui")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("absolute external UI root rejected: %v", err)
	}
}

func TestValidateRejectsRelativeExternalUIRoot(t *testing.T) {
	cfg := validProductionConfig()
	cfg.ExternalUIRoot = filepath.FromSlash("ui/releases")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "externalUiRoot") {
		t.Fatalf("relative external UI root error = %v", err)
	}
}

func TestValidateRejectsUnsafeProxyRuntimeRanges(t *testing.T) {
	cfg := validProductionConfig()
	cfg.MaxProxyGroups = 1
	cfg.VLESSPortStart, cfg.VLESSPortEnd = 20000, 20999
	cfg.MixedPortStart, cfg.MixedPortEnd = 30000, 30999
	cfg.XrayPath = "/usr/local/x-ui/bin/xray-linux-amd64"
	cfg.ProbeHost = "api.ipify.org"
	cfg.MixedSourceCIDRs = []string{"0.0.0.0/0"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("global mixed CIDR was accepted")
	}
}

func TestValidateSupportsAtMostSixtyFourOnlineEgresses(t *testing.T) {
	cfg := validProductionConfig()
	cfg.MaxProxyGroups = 64
	if err := cfg.Validate(); err != nil {
		t.Fatalf("64 online egresses rejected: %v", err)
	}
	cfg.MaxProxyGroups = 65
	if err := cfg.Validate(); err == nil {
		t.Fatal("65 online egresses accepted")
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

func TestValidateRejectsNonLoopbackAimiliControlURL(t *testing.T) {
	cfg := validProductionConfig()
	cfg.AimiliControlURL = "http://192.0.2.10:8790/"

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

func TestValidateAcceptsOnlyFixedSameOriginBackendPaths(t *testing.T) {
	cfg := validProductionConfig()
	cfg.AimiliBackendURL = "/aimili-native/"
	cfg.ExpertModeURL = "/xui-native/"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"https://evil.invalid/", "//evil.invalid/", "/native/?target=bad", "/native"} {
		candidate := cfg
		candidate.AimiliBackendURL = value
		if err := candidate.Validate(); err == nil {
			t.Fatalf("unsafe backend path accepted: %q", value)
		}
	}
}

func TestValidateRequiresBackendPathsAsAPair(t *testing.T) {
	cfg := validProductionConfig()
	cfg.AimiliBackendURL = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("partial backend path configuration was accepted")
	}
	cfg.ExpertModeURL = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("fully disabled backend login rejected: %v", err)
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
	t.Setenv("GATEWAY_AIMILI_CONTROL_URL", "http://[::1]:8899/")
	t.Setenv("GATEWAY_AIMILI_CONTROL_TOKEN_FILE", filepath.FromSlash("secrets/aimili.token"))

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
	if cfg.AimiliControlURL != "http://[::1]:8899/" {
		t.Fatalf("Aimili control URL = %q", cfg.AimiliControlURL)
	}
	if cfg.AimiliControlTokenFile != filepath.FromSlash("secrets/aimili.token") {
		t.Fatalf("Aimili control token file = %q", cfg.AimiliControlTokenFile)
	}
}

func TestValidateRequiresSiblingUpdateSpoolDirectories(t *testing.T) {
	cfg := validProductionConfig()
	cfg.UpdateRequestDir = filepath.FromSlash("/var/lib/aimili-gateway/update-spool/requests")
	if err := cfg.Validate(); err == nil {
		t.Fatal("partial update spool accepted")
	}
	cfg.UpdateResultDir = filepath.FromSlash("/var/lib/aimili-gateway/update-spool/results")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid update spool rejected: %v", err)
	}
}

func validProductionConfig() Config {
	return Config{
		ListenAddress:          "127.0.0.1:9080",
		PublicOrigin:           "https://console.example.test",
		DatabasePath:           filepath.FromSlash("data/gateway.db"),
		MasterKeyFile:          filepath.FromSlash("data/master.key"),
		AimiliAddress:          "127.0.0.1:8787",
		AimiliControlURL:       "http://127.0.0.1:8790/",
		AimiliControlTokenFile: filepath.FromSlash("data/aimili-control.token"),
		XUIBaseURL:             "http://127.0.0.1:2001/",
		XUICredentialsFile:     filepath.FromSlash("data/xui-automation.json"),
		ProtocolRequestDir:     filepath.FromSlash("/var/lib/aimili-gateway/protocol-spool/requests"),
		ProtocolResultDir:      filepath.FromSlash("/var/lib/aimili-gateway/protocol-spool/results"),
		ProtocolTimeoutSeconds: 180,
		ExpertModeURL:          "/expert/",
		AimiliBackendURL:       "/aimili-native/",
	}.WithRuntimeDefaults()
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"GATEWAY_LISTEN_ADDRESS",
		"GATEWAY_PUBLIC_ORIGIN",
		"GATEWAY_DATABASE_PATH",
		"GATEWAY_MASTER_KEY_FILE",
		"GATEWAY_AIMILI_ADDRESS",
		"GATEWAY_AIMILI_CONTROL_URL",
		"GATEWAY_AIMILI_CONTROL_TOKEN_FILE",
		"GATEWAY_XUI_BASE_URL",
		"GATEWAY_XUI_CREDENTIALS_FILE",
		"GATEWAY_EXPERT_MODE_URL",
		"GATEWAY_AIMILI_BACKEND_URL",
		"GATEWAY_PROTOCOL_REQUEST_DIR",
		"GATEWAY_PROTOCOL_RESULT_DIR",
		"GATEWAY_UPDATE_REQUEST_DIR",
		"GATEWAY_UPDATE_RESULT_DIR",
	} {
		t.Setenv(name, "")
	}
}
