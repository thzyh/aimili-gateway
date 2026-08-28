package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/accountsync"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type recordingReconciler struct{ called chan struct{} }

func (r recordingReconciler) Reconcile(context.Context) orchestrator.ReconcileResult {
	close(r.called)
	return orchestrator.ReconcileResult{}
}

func TestInitialReconcileRunsInBackground(t *testing.T) {
	called := make(chan struct{})
	startInitialReconcile(t.Context(), recordingReconciler{called: called})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("initial reconciliation did not start")
	}
}

func TestMaintenanceConfigUsesGatewayLifetimeAndReconciler(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	called := make(chan struct{})
	config := newMaintenanceConfig(lifetime, 3, recordingReconciler{called: called})
	if config.MaxOnline != 3 || config.LifetimeContext != lifetime || config.Reconcile == nil {
		t.Fatalf("maintenance config = %#v", config)
	}
	config.Reconcile(context.Background())
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("maintenance reconcile callback did not invoke proxy reconciler")
	}
	cancel()
	if lifetime.Err() == nil {
		t.Fatal("maintenance lifetime did not follow Gateway cancellation")
	}
}

func TestLegacyXUICredentialsMigrateToEncryptedUnifiedCredentialsWithoutChangingResetState(t *testing.T) {
	directory := t.TempDir()
	database, err := store.Open(t.Context(), filepath.Join(directory, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	path := filepath.Join(directory, "xui-automation.json")
	if err := os.WriteFile(path, []byte(`{"username":"legacy-owner","password":"legacy-password-marker"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	if err := accountsync.MigrateLegacyCredentials(t.Context(), database, path, key); err != nil {
		t.Fatal(err)
	}
	credentials, err := database.LoadUnifiedCredentials(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(credentials.Password)
	if credentials.Username != "legacy-owner" || string(credentials.Password) != "legacy-password-marker" {
		t.Fatal("legacy credentials did not migrate")
	}
	state, err := database.GetAccountSyncState(t.Context())
	if err != nil || state.Status != store.AccountSyncResetRequired {
		t.Fatalf("sync state = %#v err=%v", state, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("migration removed rollback input before deployment verification")
	}
}

func TestAccountDriftChecksRunAfterInitialDelayAndStopWithContext(t *testing.T) {
	checker := &recordingAccountChecker{called: make(chan struct{}, 2)}
	ctx, cancel := context.WithCancel(context.Background())
	done := startAccountDriftChecks(ctx, checker, time.Millisecond, 5*time.Millisecond)
	select {
	case <-checker.called:
	case <-time.After(time.Second):
		t.Fatal("account drift check did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("account drift checker did not stop")
	}
}

type recordingAccountChecker struct{ called chan struct{} }

func (checker *recordingAccountChecker) Check(context.Context) error {
	checker.called <- struct{}{}
	return nil
}

func TestNewProvidesHealthHandlerAndClosesIdempotently(t *testing.T) {
	directory := t.TempDir()
	masterKeyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(masterKeyPath, []byte(strings.Repeat("k", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.Context(), config.Config{
		ListenAddress:          "127.0.0.1:9080",
		PublicOrigin:           "https://console.example.test",
		DatabasePath:           filepath.Join(directory, "gateway.db"),
		MasterKeyFile:          masterKeyPath,
		AimiliAddress:          "127.0.0.1:8787",
		AimiliControlURL:       "http://127.0.0.1:8790/",
		AimiliControlTokenFile: filepath.Join(directory, "aimili-control.token"),
		XUIBaseURL:             "http://127.0.0.1:2001/panel-fixture/",
		XUICredentialsFile:     filepath.Join(directory, "xui-automation.json"),
		AimiliBackendURL:       "/aimili-fixture/",
		ExpertModeURL:          "/expert-fixture/",
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

func TestAPIRouteTakesPrecedenceOverSPAFallback(t *testing.T) {
	application := newTestApplication(t)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), `<div id="app"></div>`) {
		t.Fatal("missing API route was served by SPA fallback")
	}
}

func TestNewRejectsInvalidMasterKeyLength(t *testing.T) {
	directory := t.TempDir()
	masterKeyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(masterKeyPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(t.Context(), config.Config{
		ListenAddress:          "127.0.0.1:9080",
		PublicOrigin:           "https://console.example.test",
		DatabasePath:           filepath.Join(directory, "gateway.db"),
		MasterKeyFile:          masterKeyPath,
		AimiliAddress:          "127.0.0.1:8787",
		AimiliControlURL:       "http://127.0.0.1:8790/",
		AimiliControlTokenFile: filepath.Join(directory, "aimili-control.token"),
		XUIBaseURL:             "http://127.0.0.1:2001/panel-fixture/",
		XUICredentialsFile:     filepath.Join(directory, "xui-automation.json"),
	})
	if err == nil {
		t.Fatal("invalid master key accepted")
	}
}

func TestNewRejectsPartialAdapterSecretConfiguration(t *testing.T) {
	directory := t.TempDir()
	masterKeyPath := filepath.Join(directory, "master.key")
	tokenPath := filepath.Join(directory, "aimili-control.token")
	if err := os.WriteFile(masterKeyPath, []byte(strings.Repeat("k", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("test-control-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.Context(), config.Config{
		ListenAddress: "127.0.0.1:9080", PublicOrigin: "https://console.example.test",
		DatabasePath: filepath.Join(directory, "gateway.db"), MasterKeyFile: masterKeyPath,
		AimiliAddress: "127.0.0.1:8787", AimiliControlURL: "http://127.0.0.1:8790/", AimiliControlTokenFile: tokenPath,
		XUIBaseURL: "http://127.0.0.1:2001/panel-fixture/", XUICredentialsFile: filepath.Join(directory, "missing-xui.json"),
	})
	if err == nil {
		_ = application.Close()
		t.Fatal("partial adapter secret configuration was accepted")
	}
}

func TestNewSupportsExplicitLocalTestConfiguration(t *testing.T) {
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

func newTestApplication(t *testing.T) *App {
	t.Helper()
	directory := t.TempDir()
	masterKeyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(masterKeyPath, []byte(strings.Repeat("k", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.Context(), config.Config{
		ListenAddress:          "127.0.0.1:9080",
		PublicOrigin:           "https://console.example.test",
		DatabasePath:           filepath.Join(directory, "gateway.db"),
		MasterKeyFile:          masterKeyPath,
		AimiliAddress:          "127.0.0.1:8787",
		AimiliControlURL:       "http://127.0.0.1:8790/",
		AimiliControlTokenFile: filepath.Join(directory, "aimili-control.token"),
		XUIBaseURL:             "http://127.0.0.1:2001/panel-fixture/",
		XUICredentialsFile:     filepath.Join(directory, "xui-automation.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application
}
