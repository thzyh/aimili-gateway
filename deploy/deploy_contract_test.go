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
		"Environment=GOMEMLIMIT=64MiB",
		"MemoryHigh=64M",
		"MemoryMax=96M",
		"TasksMax=64",
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
	if config.AimiliControlURL != "http://127.0.0.1:8790/" || !strings.HasPrefix(config.AimiliControlTokenFile, "/run/credentials/") || !strings.HasPrefix(config.XUICredentialsFile, "/run/credentials/") || config.MaxProxyGroups != 64 || len(config.MixedSourceCIDRs) != 1 {
		t.Fatal("example configuration is missing the V1-C online-pool adapter contract")
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

func TestV1DOperationalScriptsAreSecretSafeAndRollbackOrdered(t *testing.T) {
	preflight := readAsset(t, "bin/aimili-gateway-v1d-preflight")
	verify := readAsset(t, "bin/aimili-gateway-v1d-verify")
	backup := readAsset(t, "bin/aimili-gateway-v1d-backup")
	rollback := readAsset(t, "bin/aimili-gateway-v1d-rollback")
	for name, script := range map[string]string{"preflight": preflight, "verify": verify, "backup": backup, "rollback": rollback} {
		if strings.Contains(script, "\r") || !strings.Contains(script, "set -euo pipefail") || strings.Contains(script, "set -x") {
			t.Fatalf("%s script lacks safe shell contract", name)
		}
		for _, forbidden := range []string{"cat /etc/aimili-gateway/config.json", "echo $password", "echo $cookie", "x-ui.db"} {
			if strings.Contains(strings.ToLower(script), strings.ToLower(forbidden)) {
				t.Fatalf("%s script contains unsafe behavior %q", name, forbidden)
			}
		}
	}
	for name, script := range map[string]string{"preflight": preflight, "verify": verify} {
		for _, forbidden := range []string{
			`--header "Authorization: Bearer $(`,
			`--header "Authorization: Bearer ${`,
		} {
			if strings.Contains(script, forbidden) {
				t.Fatalf("%s exposes the control token through curl argv", name)
			}
		}
		for _, required := range []string{"mktemp -d", "chmod 0700", `--header @"$auth_header"`} {
			if !strings.Contains(script, required) {
				t.Fatalf("%s does not use a root-only curl header file: missing %q", name, required)
			}
		}
	}
	for _, required := range []string{"aimili-gateway.service", "aimilivpn.service", "x-ui.service", "caddy.service", "MemAvailable", "SwapTotal", "maxProxyGroups", "control/v1/capabilities", "PASS"} {
		if !strings.Contains(preflight, required) {
			t.Fatalf("preflight missing %q", required)
		}
	}
	for _, required := range []string{
		"aimili-gateway.db", "gateway-master-key", "aimili-ui", "xui-config", "xui-automation", "Caddyfile",
		"aimili-gateway-admin", "aimili-gateway-account", "aimili-gateway.service",
		"aimilivpn-git-branch", "aimilivpn-git-head", "sha256sum", "chmod 0700",
	} {
		if !strings.Contains(backup, required) {
			t.Fatalf("backup missing %q", required)
		}
	}
	order := []string{
		"restore_binary", "restore_service_assets", "restore_aimilivpn_checkout",
		"restore_config", "restore_database", "daemon_reload",
		"start_aimilivpn", "start_xui", "start_gateway", "start_caddy",
	}
	last := -1
	for _, marker := range order {
		index := strings.Index(rollback, marker)
		if index <= last {
			t.Fatalf("rollback marker %q is missing or out of order", marker)
		}
		last = index
	}
	if strings.Contains(rollback, "git reset") {
		t.Fatal("rollback must not rewrite the recorded AimiliVPN branch")
	}
	for _, required := range []string{"/healthz", "control/v1/capabilities", "HTTP", "PASS", "FAIL"} {
		if !strings.Contains(verify, required) {
			t.Fatalf("verify missing %q", required)
		}
	}
}

func TestV1DCaddyUsesOnlyFixedBackendSubpaths(t *testing.T) {
	fragment := readAsset(t, "caddy/AimiliGateway.Caddyfile")
	for _, required := range []string{"handle /EXISTING_AIMILIVPN_ROUTE/*", "reverse_proxy 127.0.0.1:8787", "handle /EXISTING_3X_UI_ROUTE/*", "reverse_proxy 127.0.0.1:2001"} {
		if !strings.Contains(fragment, required) {
			t.Fatalf("Caddy fragment missing V1-D route %q", required)
		}
	}
	for _, forbidden := range []string{"handle_path /EXISTING_AIMILIVPN_ROUTE/*", "basic_auth", "forward_auth"} {
		if strings.Contains(strings.ToLower(fragment), forbidden) {
			t.Fatalf("Caddy fragment adds an unsupported auth layer %q", forbidden)
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
		"LoadCredential=aimili-control-token:/etc/aimilivpn/control.token",
		"LoadCredential=xui-automation:/etc/aimili-gateway/xui-automation.json",
		"GATEWAY_CONFIG=/etc/aimili-gateway/config.json",
		`credential_directory="/run/credentials/${unit_name}.service"`,
		`GATEWAY_MASTER_KEY_FILE="$credential_directory/gateway-master-key"`,
		`GATEWAY_AIMILI_CONTROL_TOKEN_FILE="$credential_directory/aimili-control-token"`,
		`GATEWAY_XUI_CREDENTIALS_FILE="$credential_directory/xui-automation"`,
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
		"IPAddressDeny=any",
		"IPAddressAllow=localhost",
		"NoNewPrivileges=yes",
		"/usr/local/bin/aimili-gateway-admin",
		"account",
		"/usr/bin/systemctl try-restart aimili-gateway.service",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("account command missing %q", required)
		}
	}
	if !strings.Contains(script, `unit_name="aimili-gateway-account-`) || !strings.Contains(script, `--unit="$unit_name"`) {
		t.Fatal("account command does not use a unique transient unit name")
	}
	for _, forbidden := range []string{"bash -c", "sh -c", "eval ", "curl ", "wget ", "$@", "=%d/"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("account command contains unsafe behavior %q", forbidden)
		}
	}
}

func TestV1CRemoteDeploymentIsIncrementalAndRollbackSafe(t *testing.T) {
	script := readAsset(t, "../scripts/deploy-online-pools-v1c-remote.sh")
	for _, required := range []string{
		"sha256sum",
		"bundle verify",
		"stash push --include-untracked",
		"merge --ff-only FETCH_HEAD",
		"Environment=OPENVPN_TEST_CONCURRENCY=1",
		"Environment=TCP_PRESCREEN_CONCURRENCY=8",
		"Environment=NODE_TEST_BATCH_SIZE=1",
		"Environment=MAX_EXIT_SLOTS=4",
		"Environment=COLLECTOR_INITIAL_DELAY_SECONDS=120",
		"Environment=COLLECTOR_FAILURE_BACKOFF_SECONDS=600",
		"MemoryHigh=180M",
		"MemoryMax=220M",
		"TasksMax=48",
		"Restart=on-failure",
		`d["maxProxyGroups"] = capacity`,
		"systemctl restart aimilivpn.service",
		"systemctl start aimili-gateway.service",
		"http://127.0.0.1:9080/healthz",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("V1-C remote deployment missing %q", required)
		}
	}
	for _, forbidden := range []string{"reset --hard", "checkout --force", "curl github", "rm -rf /opt/aimilivpn"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("V1-C remote deployment contains destructive behavior %q", forbidden)
		}
	}
}

func TestV1CFreshGatewayBootstrapPreservesServiceBoundaries(t *testing.T) {
	script := readAsset(t, "../scripts/bootstrap-online-pools-v1c-remote.sh")
	for _, required := range []string{
		"sha256sum",
		"id -u aimili-gateway",
		"systemd-creds encrypt",
		"install -d -m 0750 -o root -g aimili-gateway /etc/aimili-gateway",
		"/etc/aimilivpn/control.token",
		"/etc/aimili-gateway/xui-automation.json",
		"GATEWAY_CONFIG=/etc/aimili-gateway/config.json",
		"'/usr/local/bin/aimili-gateway-admin', 'init'",
		"maxProxyGroups",
		"127.0.0.1:9080",
		"reverse_proxy 127.0.0.1:9080",
		"caddy validate",
		"systemctl enable --now aimili-gateway.service",
		"aimili-gateway-account",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("V1-C fresh bootstrap missing %q", required)
		}
	}
	for _, forbidden := range []string{"x-ui.db", "0.0.0.0/0", "::/0", "password=", "Cookie"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("V1-C fresh bootstrap contains unsafe behavior %q", forbidden)
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
