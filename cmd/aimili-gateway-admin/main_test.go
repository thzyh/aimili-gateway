package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/url"
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
	if strings.Count(output.String(), "otpauth://") != 1 {
		t.Fatal("enrollment URI was not emitted exactly once")
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
	totpSecret, err := auth.Open(masterKey, admin.TOTPSecretCiphertext)
	if err != nil {
		t.Fatal(err)
	}

	enrollment, err := url.Parse(extractEnrollmentURI(t, output.String()))
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.Path != "/Aimili Gateway:owner local" {
		t.Fatalf("enrollment label = %q", enrollment.Path)
	}
	query := enrollment.Query()
	if query.Get("issuer") != "Aimili Gateway" || query.Get("algorithm") != "SHA1" || query.Get("digits") != "6" || query.Get("period") != "30" {
		t.Fatal("enrollment parameters are incomplete")
	}
	uriSecret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(query.Get("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uriSecret, totpSecret) {
		t.Fatal("enrollment secret does not match encrypted storage")
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

func extractEnrollmentURI(t *testing.T, output string) string {
	t.Helper()
	start := strings.Index(output, "otpauth://")
	if start < 0 {
		t.Fatal("enrollment URI missing")
	}
	end := strings.IndexByte(output[start:], '\n')
	if end < 0 {
		return strings.TrimSpace(output[start:])
	}
	return strings.TrimSpace(output[start : start+end])
}
