package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnitIsUnprivilegedAndHardened(t *testing.T) {
	unit := readAsset(t, "systemd/aimili-gateway.service")
	for _, required := range []string{
		"User=aimili-gateway",
		"Group=aimili-gateway",
		"NoNewPrivileges=true",
		"PrivateTmp=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"ReadWritePaths=/var/lib/aimili-gateway",
		"CapabilityBoundingSet=",
		"LoadCredentialEncrypted=gateway-master-key:",
		"Environment=GATEWAY_CONFIG=/etc/aimili-gateway/config.json",
		"Environment=GATEWAY_MASTER_KEY_FILE=%d/gateway-master-key",
		"LoadCredential=aimili-control-token:/etc/aimilivpn/control.token",
		"LoadCredential=xui-automation:/etc/aimili-gateway/xui-automation.json",
		"Environment=GATEWAY_AIMILI_CONTROL_TOKEN_FILE=%d/aimili-control-token",
		"Environment=GATEWAY_XUI_CREDENTIALS_FILE=%d/xui-automation",
	} {
		if !strings.Contains(unit, required) {
			t.Fatalf("systemd unit missing %q", required)
		}
	}
	for _, forbidden := range []string{"User=root", "/bin/sh", "/bin/bash", "systemctl", "caddy reload", "sudo"} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("systemd unit contains forbidden capability %q", forbidden)
		}
	}
}

func TestExampleConfigUsesOnlyLoopbackAndPlaceholders(t *testing.T) {
	contents := readAsset(t, "config/config.example.json")
	var config struct {
		ListenAddress          string   `json:"listenAddress"`
		PublicOrigin           string   `json:"publicOrigin"`
		AimiliAddress          string   `json:"aimiliAddress"`
		XUIBaseURL             string   `json:"xuiBaseUrl"`
		ExpertModeURL          string   `json:"expertModeUrl"`
		AimiliControlURL       string   `json:"aimiliControlUrl"`
		AimiliControlTokenFile string   `json:"aimiliControlTokenFile"`
		XUICredentialsFile     string   `json:"xuiCredentialsFile"`
		MaxProxyGroups         int      `json:"maxProxyGroups"`
		MixedSourceCIDRs       []string `json:"mixedSourceCidrs"`
	}
	if err := json.Unmarshal([]byte(contents), &config); err != nil {
		t.Fatal(err)
	}
	if config.ListenAddress != "127.0.0.1:9080" || !strings.HasPrefix(config.AimiliAddress, "127.0.0.1:") || !strings.HasPrefix(config.XUIBaseURL, "http://127.0.0.1:") {
		t.Fatal("example configuration exposes a non-loopback service")
	}
	if config.PublicOrigin != "https://gateway.example.invalid" {
		t.Fatal("example public origin is not the reserved placeholder")
	}
	if config.ExpertModeURL != "/EXISTING_3X_UI_ROUTE/" || !strings.Contains(config.XUIBaseURL, "EXISTING_3X_UI_BASE_PATH") {
		t.Fatal("example configuration does not use explicit path placeholders")
	}
	if config.AimiliControlURL != "http://127.0.0.1:8790/" || !strings.HasPrefix(config.AimiliControlTokenFile, "/run/credentials/") || !strings.HasPrefix(config.XUICredentialsFile, "/run/credentials/") || config.MaxProxyGroups != 1 || len(config.MixedSourceCIDRs) != 1 {
		t.Fatal("example configuration is missing the single-group adapter contract")
	}
}

func TestCaddyFragmentPreservesExistingRoutesBeforeGatewayFallback(t *testing.T) {
	fragment := readAsset(t, "caddy/AimiliGateway.Caddyfile")
	expert := strings.Index(fragment, "# EXISTING_3X_UI_EXPERT_ROUTE")
	subscription := strings.Index(fragment, "# EXISTING_SUBSCRIPTION_ROUTE")
	api := strings.Index(fragment, "handle /api/v1/*")
	fallback := strings.LastIndex(fragment, "handle {")
	if expert < 0 || subscription < 0 || api < 0 || fallback < 0 || expert > api || subscription > api || api > fallback {
		t.Fatal("Caddy route order is unsafe")
	}
	for _, required := range []string{
		"reverse_proxy 127.0.0.1:9080",
		"X-Content-Type-Options nosniff",
		"X-Frame-Options DENY",
		"Referrer-Policy no-referrer",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(fragment, required) {
			t.Fatalf("Caddy fragment missing %q", required)
		}
	}
	lower := strings.ToLower(fragment)
	for _, forbidden := range []string{"basic_auth", "basicauth", "single sign-on", "true sso"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("Caddy fragment contains forbidden claim or behavior %q", forbidden)
		}
	}
}

func TestAccountCommandUsesRestrictedTransientUnit(t *testing.T) {
	script := readAsset(t, "bin/aimili-gateway-account")
	if strings.Contains(script, "\r") {
		t.Fatal("account command must use LF line endings for its Linux shebang")
	}
	for _, required := range []string{
		"/usr/bin/systemd-run",
		"--pty",
		"--wait",
		"--collect",
		"User=aimili-gateway",
		"Group=aimili-gateway",
		"LoadCredentialEncrypted=gateway-master-key:",
		"GATEWAY_CONFIG=/etc/aimili-gateway/config.json",
		"GATEWAY_MASTER_KEY_FILE=%d/gateway-master-key",
		"IPAddressDeny=any",
		"NoNewPrivileges=yes",
		"/usr/local/bin/aimili-gateway-admin",
		"account",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("account command missing %q", required)
		}
	}
	for _, forbidden := range []string{"bash -c", "sh -c", "eval ", "curl ", "wget ", "$@", "--unit="} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("account command contains unsafe behavior %q", forbidden)
		}
	}
}

func readAsset(t *testing.T, relativePath string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.FromSlash(relativePath))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
