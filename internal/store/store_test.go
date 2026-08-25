package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreAllowsExactlyOneAdmin(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	first := Admin{
		Username:             "owner",
		PasswordHash:         []byte("encoded-hash"),
		TOTPEnabled:          true,
		TOTPSecretCiphertext: []byte("encrypted-secret"),
		CreatedAt:            now,
		SecurityUpdatedAt:    now,
	}
	if err := store.CreateAdmin(ctx, first); err != nil {
		t.Fatal(err)
	}

	err := store.CreateAdmin(ctx, Admin{
		Username:             "second",
		PasswordHash:         []byte("other-hash"),
		TOTPEnabled:          true,
		TOTPSecretCiphertext: []byte("other-secret"),
		CreatedAt:            now,
		SecurityUpdatedAt:    now,
	})
	if !errors.Is(err, ErrAdminExists) {
		t.Fatalf("expected ErrAdminExists, got %v", err)
	}

	got, err := store.GetAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != first.Username || string(got.PasswordHash) != string(first.PasswordHash) {
		t.Fatalf("admin round trip = %#v", got)
	}
}

func TestMigrationTwoPreservesExistingTOTP(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gateway.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(ctx, `
		CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES(1, 1700000000000);
		CREATE TABLE admin (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash BLOB NOT NULL CHECK (length(password_hash) > 0),
			totp_secret_ciphertext BLOB NOT NULL CHECK (length(totp_secret_ciphertext) > 0),
			created_at INTEGER NOT NULL,
			security_updated_at INTEGER NOT NULL
		);
		INSERT INTO admin VALUES(1, 'owner', X'0102', X'0304', 1700000000000, 1700000000000);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	opened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	admin, err := opened.GetAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !admin.TOTPEnabled || string(admin.TOTPSecretCiphertext) != string([]byte{3, 4}) {
		t.Fatal("migrated administrator silently lost TOTP")
	}
	var count int
	if err := opened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration 2 count = %d", count)
	}
}

func TestCreateAdminAllowsDisabledTOTP(t *testing.T) {
	database := openTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := database.CreateAdmin(context.Background(), Admin{
		Username:          "owner",
		PasswordHash:      []byte("encoded-hash"),
		TOTPEnabled:       false,
		CreatedAt:         now,
		SecurityUpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := database.GetAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if admin.TOTPEnabled || len(admin.TOTPSecretCiphertext) != 0 {
		t.Fatal("disabled TOTP administrator has a secret")
	}
}

func TestUpdateAdminSecurityAndRevokeSessionsIsAtomic(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	admin := Admin{
		Username:             "owner",
		PasswordHash:         []byte("old-hash"),
		TOTPEnabled:          true,
		TOTPSecretCiphertext: []byte("old-secret"),
		CreatedAt:            now,
		SecurityUpdatedAt:    now,
	}
	if err := database.CreateAdmin(ctx, admin); err != nil {
		t.Fatal(err)
	}
	session := testSession("security-update", now)
	if err := database.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	updatedAt := now.Add(time.Minute)
	if err := database.UpdateAdminSecurityAndRevokeSessions(ctx, AdminSecurityUpdate{
		ExpectedSecurityUpdatedAt: now,
		Username:                  "renamed",
		PasswordHash:              []byte("new-hash"),
		TOTPEnabled:               false,
		Action:                    "account.password_updated",
		UpdatedAt:                 updatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "renamed" || string(got.PasswordHash) != "new-hash" || got.TOTPEnabled || len(got.TOTPSecretCiphertext) != 0 || !got.SecurityUpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated administrator = %#v", got)
	}
	if _, err := database.GetSession(ctx, session.TokenHash, updatedAt); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("security update did not revoke session: %v", err)
	}
	var action, fingerprint string
	if err := database.db.QueryRowContext(ctx, `SELECT action, resource_fingerprint FROM audit_events`).Scan(&action, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if action != "account.password_updated" || fingerprint != "local-admin" {
		t.Fatalf("audit action = %q, fingerprint = %q", action, fingerprint)
	}
}

func TestUpdateAdminSecurityRejectsStaleVersion(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := database.CreateAdmin(ctx, Admin{
		Username:          "owner",
		PasswordHash:      []byte("old-hash"),
		CreatedAt:         now,
		SecurityUpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	err := database.UpdateAdminSecurityAndRevokeSessions(ctx, AdminSecurityUpdate{
		ExpectedSecurityUpdatedAt: now.Add(-time.Second),
		Username:                  "renamed",
		PasswordHash:              []byte("new-hash"),
		Action:                    "account.username_updated",
		UpdatedAt:                 now.Add(time.Minute),
	})
	if !errors.Is(err, ErrAdminChanged) {
		t.Fatalf("expected ErrAdminChanged, got %v", err)
	}
	admin, getErr := database.GetAdmin(ctx)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if admin.Username != "owner" || string(admin.PasswordHash) != "old-hash" {
		t.Fatal("stale update changed administrator")
	}
}

func TestRevokedSessionCannotBeLoaded(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	session := testSession("revoked", now)

	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeSession(ctx, session.TokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(ctx, session.TokenHash, now.Add(time.Minute)); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected revoked session to be hidden, got %v", err)
	}
}

func TestExpiredSessionCannotBeLoaded(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	session := testSession("expired", now)
	session.ExpiresAt = now.Add(time.Minute)

	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(ctx, session.TokenHash, now.Add(2*time.Minute)); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected expired session to be hidden, got %v", err)
	}
}

func TestMigrationsAreIdempotentAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gateway.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	var count int
	if err := second.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration row count = %d", count)
	}
}

func TestOpenConfiguresRequiredSQLitePragmas(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	var foreignKeys int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d", foreignKeys)
	}

	var journalMode string
	if err := store.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q", journalMode)
	}

	var busyTimeout int
	if err := store.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d", busyTimeout)
	}
}

func TestConcurrentSessionInsertsRemainDistinct(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	const total = 8
	errorsCh := make(chan error, total)
	var wait sync.WaitGroup
	for index := 0; index < total; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errorsCh <- store.CreateSession(ctx, testSession(string(rune('a'+index)), now))
		}(index)
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != total {
		t.Fatalf("session count = %d", count)
	}
}

func TestFailedAuditInsertLeavesNoPartialRow(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	if err := store.AppendAudit(ctx, AuditEvent{
		Action:              "auth.login",
		ResourceType:        "gateway",
		ResourceFingerprint: "local",
		Result:              AuditSuccess,
		CreatedAt:           now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAudit(ctx, AuditEvent{
		Action:              "auth.login",
		ResourceType:        "gateway",
		ResourceFingerprint: "local",
		Result:              AuditResult("invalid-result"),
		CreatedAt:           now,
	}); err == nil {
		t.Fatal("expected invalid audit result to fail")
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit row count = %d", count)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testSession(label string, now time.Time) Session {
	return Session{
		TokenHash:     sha256.Sum256([]byte("token-" + label)),
		CSRFTokenHash: sha256.Sum256([]byte("csrf-" + label)),
		CreatedAt:     now,
		LastActiveAt:  now,
		ExpiresAt:     now.Add(12 * time.Hour),
	}
}
