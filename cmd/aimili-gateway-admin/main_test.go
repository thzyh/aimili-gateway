package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/auth"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestInitCreatesEncryptedSingleAdminWithoutEchoingPassword(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	password := "local-only-test-password"
	input := strings.NewReader("owner local\n" + password + "\n" + password + "\n")
	var output bytes.Buffer
	var errorOutput bytes.Buffer

	if code := run([]string{"init"}, input, &output, &errorOutput, environment.now); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Contains(output.String(), password) || strings.Contains(errorOutput.String(), password) {
		t.Fatal("password was written to command output")
	}
	if strings.Contains(output.String(), "otpauth://") {
		t.Fatal("new administrator unexpectedly emitted a TOTP enrollment URI")
	}

	database, err := store.Open(context.Background(), environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	admin, err := database.GetAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	matched, err := auth.VerifyPassword(string(admin.PasswordHash), []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("stored password hash did not verify")
	}
	masterKey, err := os.ReadFile(environment.masterKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(masterKey) != 32 {
		t.Fatalf("master key length = %d", len(masterKey))
	}
	if admin.TOTPEnabled || len(admin.TOTPSecretCiphertext) != 0 {
		t.Fatal("new administrator did not default to password-only authentication")
	}
}

func TestLoadAccountXUICredentialsUsesEncryptedUnifiedCredentialsAfterSync(t *testing.T) {
	directory := t.TempDir()
	database, err := store.Open(t.Context(), filepath.Join(directory, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	legacyPath := filepath.Join(directory, "xui-automation.json")
	if err := os.WriteFile(legacyPath, []byte(`{"username":"owner","password":"stale-password-marker"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	if err := database.PutCredential(t.Context(), store.UnifiedUsernamePurpose, []byte("owner"), key); err != nil {
		t.Fatal(err)
	}
	if err := database.PutCredential(t.Context(), store.UnifiedPasswordPurpose, []byte("current-password-marker"), key); err != nil {
		t.Fatal(err)
	}
	if err := database.SetAccountSyncState(t.Context(), store.AccountSyncState{
		Status:              store.AccountSyncSynced,
		UsernameFingerprint: "committed-fingerprint",
	}); err != nil {
		t.Fatal(err)
	}

	credentials, err := loadAccountXUICredentials(t.Context(), database, legacyPath, key)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Username != "owner" || credentials.Password != "current-password-marker" {
		t.Fatal("account command did not use committed encrypted credentials")
	}
}

func TestInitRejectsSecondAdministrator(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	firstInput := strings.NewReader("owner\nlocal-only-test-password\nlocal-only-test-password\n")
	if code := run([]string{"init"}, firstInput, &bytes.Buffer{}, &bytes.Buffer{}, environment.now); code != 0 {
		t.Fatalf("first exit code = %d", code)
	}

	var output bytes.Buffer
	secondInput := strings.NewReader("second\nsecond-local-test-password\nsecond-local-test-password\n")
	if code := run([]string{"init"}, secondInput, &output, &bytes.Buffer{}, environment.now); code == 0 {
		t.Fatal("second initialization succeeded")
	}
	if strings.Contains(output.String(), "otpauth://") {
		t.Fatal("second initialization emitted an enrollment URI")
	}
}

func TestInitRejectsInvalidPasswords(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		password string
	}{
		{name: "too short", password: "short-test"},
		{name: "leading whitespace", password: " local-test-password"},
		{name: "trailing whitespace", password: "local-test-password "},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			environment := newAdminTestEnvironment(t)
			input := strings.NewReader("owner\n" + testCase.password + "\n" + testCase.password + "\n")
			if code := run([]string{"init"}, input, &bytes.Buffer{}, &bytes.Buffer{}, environment.now); code == 0 {
				t.Fatal("invalid password accepted")
			}
			if _, err := os.Stat(environment.masterKeyPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("master key was created for invalid input")
			}
		})
	}
}

func TestRevokeSessionsDoesNotModifyAdministratorOrMasterKey(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	ctx := context.Background()
	database, err := store.Open(ctx, environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	admin := store.Admin{
		Username:             "owner",
		PasswordHash:         []byte("encoded-test-hash"),
		TOTPEnabled:          true,
		TOTPSecretCiphertext: []byte("encrypted-test-secret"),
		CreatedAt:            environment.now(),
		SecurityUpdatedAt:    environment.now(),
	}
	if err := database.CreateAdmin(ctx, admin); err != nil {
		t.Fatal(err)
	}
	now := environment.now()
	session := store.Session{
		TokenHash:     sha256.Sum256([]byte("test-session-token")),
		CSRFTokenHash: sha256.Sum256([]byte("test-csrf-token")),
		CreatedAt:     now,
		LastActiveAt:  now,
		ExpiresAt:     now.Add(time.Hour),
	}
	if err := database.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(environment.masterKeyPath, masterKey, 0o600); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"revoke-sessions"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, environment.now); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	database, err = store.Open(ctx, environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.GetSession(ctx, session.TokenHash, now); !errors.Is(err, store.ErrSessionNotFound) {
		t.Fatalf("revoked session lookup error = %v", err)
	}
	gotAdmin, err := database.GetAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotAdmin.Username != admin.Username || !bytes.Equal(gotAdmin.PasswordHash, admin.PasswordHash) || !bytes.Equal(gotAdmin.TOTPSecretCiphertext, admin.TOTPSecretCiphertext) {
		t.Fatal("administrator credentials changed")
	}
	gotMasterKey, err := os.ReadFile(environment.masterKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotMasterKey, masterKey) {
		t.Fatal("master key changed")
	}
}

type adminTestEnvironment struct {
	databasePath  string
	masterKeyPath string
	now           func() time.Time
}

func newAdminTestEnvironment(t *testing.T) adminTestEnvironment {
	t.Helper()
	directory := t.TempDir()
	environment := adminTestEnvironment{
		databasePath:  filepath.Join(directory, "gateway.db"),
		masterKeyPath: filepath.Join(directory, "master.key"),
		now: func() time.Time {
			return time.Unix(1_700_000_000, 0).UTC()
		},
	}
	configPath := filepath.Join(directory, "config.json")
	contents, err := json.Marshal(map[string]string{
		"publicOrigin":  "https://console.example.test",
		"databasePath":  environment.databasePath,
		"masterKeyFile": environment.masterKeyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_CONFIG", configPath)
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
	return environment
}
