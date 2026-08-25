package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/auth"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestAccountMenuShowsOnlyAllowedStatusFields(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	database, _ := seedAccountAdmin(t, environment, false)
	database.Close()
	var output bytes.Buffer
	code := runWithDependencies(
		[]string{"account"},
		strings.NewReader("1\n0\n"),
		&output,
		&bytes.Buffer{},
		commandDependencies{Now: environment.now, Random: bytes.NewReader(bytes.Repeat([]byte{1}, 64))},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	for _, expected := range []string{"owner", "TOTP：已关闭", "账户创建时间", "安全信息更新时间"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("status output missing %q", expected)
		}
	}
	for _, forbidden := range []string{"encoded-test-hash", "encrypted-test-secret", "otpauth://"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("status output exposed %q", forbidden)
		}
	}
}

func TestAccountMenuGeneratesRandomPasswordOnceAndRevokesSessions(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	database, session := seedAccountAdmin(t, environment, false)
	defer database.Close()
	randomBytes := bytes.Repeat([]byte{0xAB}, 24)
	wantPassword := base64.RawURLEncoding.EncodeToString(randomBytes)
	var output bytes.Buffer
	code := runWithDependencies(
		[]string{"account"},
		strings.NewReader("3\n0\n"),
		&output,
		&bytes.Buffer{},
		commandDependencies{Now: environment.now, Random: bytes.NewReader(randomBytes)},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Count(output.String(), wantPassword) != 1 {
		t.Fatal("random password was not emitted exactly once")
	}
	admin := reopenAccountAdmin(t, environment)
	matched, err := auth.VerifyPassword(string(admin.PasswordHash), []byte(wantPassword))
	if err != nil || !matched {
		t.Fatal("generated password did not replace the old password")
	}
	check, err := store.Open(context.Background(), environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	if _, err := check.GetSession(context.Background(), session.TokenHash, environment.now()); !errors.Is(err, store.ErrSessionNotFound) {
		t.Fatal("password reset did not revoke sessions")
	}
}

func TestAccountMenuChangesUsernameAndRevokesSessions(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	database, session := seedAccountAdmin(t, environment, false)
	database.Close()
	code := runWithDependencies(
		[]string{"account"},
		strings.NewReader("2\nrenamed-owner\n0\n"),
		&bytes.Buffer{},
		&bytes.Buffer{},
		commandDependencies{Now: environment.now, Random: bytes.NewReader(bytes.Repeat([]byte{4}, 64))},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	admin := reopenAccountAdmin(t, environment)
	if admin.Username != "renamed-owner" {
		t.Fatalf("username = %q", admin.Username)
	}
	check, err := store.Open(context.Background(), environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	if _, err := check.GetSession(context.Background(), session.TokenHash, environment.now()); !errors.Is(err, store.ErrSessionNotFound) {
		t.Fatal("username update did not revoke sessions")
	}
}

func TestAccountMenuSetsCustomPasswordWithoutEchoingIt(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	database, _ := seedAccountAdmin(t, environment, false)
	database.Close()
	password := "new-local-only-password"
	var output bytes.Buffer
	code := runWithDependencies(
		[]string{"account"},
		strings.NewReader("4\n"+password+"\n"+password+"\n0\n"),
		&output,
		&bytes.Buffer{},
		commandDependencies{Now: environment.now, Random: bytes.NewReader(bytes.Repeat([]byte{2}, 64))},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Contains(output.String(), password) {
		t.Fatal("custom password was echoed")
	}
	admin := reopenAccountAdmin(t, environment)
	matched, err := auth.VerifyPassword(string(admin.PasswordHash), []byte(password))
	if err != nil || !matched {
		t.Fatal("custom password was not stored")
	}
}

func TestAccountMenuRejectsInvalidCustomPasswords(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		password     string
		confirmation string
	}{
		{name: "too short", password: "short", confirmation: "short"},
		{name: "leading whitespace", password: " new-local-only-password", confirmation: " new-local-only-password"},
		{name: "mismatch", password: "new-local-only-password", confirmation: "different-local-password"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			environment := newAdminTestEnvironment(t)
			database, _ := seedAccountAdmin(t, environment, false)
			database.Close()
			input := "4\n" + testCase.password + "\n" + testCase.confirmation + "\n0\n"
			code := runWithDependencies(
				[]string{"account"},
				strings.NewReader(input),
				&bytes.Buffer{},
				&bytes.Buffer{},
				commandDependencies{Now: environment.now, Random: bytes.NewReader(bytes.Repeat([]byte{5}, 64))},
			)
			if code != 0 {
				t.Fatalf("exit code = %d", code)
			}
			admin := reopenAccountAdmin(t, environment)
			matched, err := auth.VerifyPassword(string(admin.PasswordHash), []byte("old-local-only-password"))
			if err != nil || !matched {
				t.Fatal("invalid custom password changed the stored password")
			}
		})
	}
}

func TestAccountMenuEnablesTOTPOnlyAfterValidNewCode(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	database, _ := seedAccountAdmin(t, environment, false)
	database.Close()
	secret := []byte("12345678901234567890")
	var output bytes.Buffer
	code := runWithDependencies(
		[]string{"account"},
		strings.NewReader("5\n287082\n0\n"),
		&output,
		&bytes.Buffer{},
		commandDependencies{Now: func() time.Time { return time.Unix(59, 0).UTC() }, Random: bytes.NewReader(secret)},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Count(output.String(), "otpauth://") != 1 {
		t.Fatal("TOTP enrollment URI was not emitted exactly once")
	}
	admin := reopenAccountAdmin(t, environment)
	if !admin.TOTPEnabled {
		t.Fatal("valid new TOTP code did not enable TOTP")
	}
	masterKey, err := os.ReadFile(environment.masterKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := auth.Open(masterKey, admin.TOTPSecretCiphertext)
	if err != nil || !bytes.Equal(opened, secret) {
		t.Fatal("new TOTP secret was not encrypted correctly")
	}
}

func TestAccountMenuRejectsInvalidTOTPWithoutChangingAccount(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	database, _ := seedAccountAdmin(t, environment, false)
	database.Close()
	code := runWithDependencies(
		[]string{"account"},
		strings.NewReader("5\n000000\n0\n"),
		&bytes.Buffer{},
		&bytes.Buffer{},
		commandDependencies{Now: func() time.Time { return time.Unix(59, 0).UTC() }, Random: bytes.NewReader([]byte("12345678901234567890"))},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	admin := reopenAccountAdmin(t, environment)
	if admin.TOTPEnabled || len(admin.TOTPSecretCiphertext) != 0 {
		t.Fatal("invalid confirmation changed TOTP state")
	}
}

func TestAccountMenuDisablesTOTPAndClearsSecret(t *testing.T) {
	environment := newAdminTestEnvironment(t)
	database, _ := seedAccountAdmin(t, environment, true)
	database.Close()
	code := runWithDependencies(
		[]string{"account"},
		strings.NewReader("6\n关闭 TOTP\n0\n"),
		&bytes.Buffer{},
		&bytes.Buffer{},
		commandDependencies{Now: environment.now, Random: bytes.NewReader(bytes.Repeat([]byte{3}, 64))},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	admin := reopenAccountAdmin(t, environment)
	if admin.TOTPEnabled || len(admin.TOTPSecretCiphertext) != 0 {
		t.Fatal("TOTP disable did not clear encrypted secret")
	}
}

func seedAccountAdmin(t *testing.T, environment adminTestEnvironment, totpEnabled bool) (*store.Store, store.Session) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := auth.HashPassword([]byte("old-local-only-password"))
	if err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(environment.masterKeyPath, masterKey, 0o600); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if totpEnabled {
		encrypted, err = auth.Seal(masterKey, []byte("12345678901234567890"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := database.CreateAdmin(ctx, store.Admin{
		Username:             "owner",
		PasswordHash:         []byte(passwordHash),
		TOTPEnabled:          totpEnabled,
		TOTPSecretCiphertext: encrypted,
		CreatedAt:            environment.now(),
		SecurityUpdatedAt:    environment.now(),
	}); err != nil {
		t.Fatal(err)
	}
	session := store.Session{
		TokenHash:     sha256.Sum256([]byte("account-session-token")),
		CSRFTokenHash: sha256.Sum256([]byte("account-session-csrf")),
		CreatedAt:     environment.now(),
		LastActiveAt:  environment.now(),
		ExpiresAt:     environment.now().Add(time.Hour),
	}
	if err := database.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	return database, session
}

func reopenAccountAdmin(t *testing.T, environment adminTestEnvironment) store.Admin {
	t.Helper()
	database, err := store.Open(context.Background(), environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	admin, err := database.GetAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return admin
}
