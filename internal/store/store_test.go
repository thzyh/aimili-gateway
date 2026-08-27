package store

import (
	"bytes"
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

func TestMigrationFivePreservesV1BProxyGroupAndOperation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gateway.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []struct {
		version int
		name    string
	}{{3, "003_country_proxy.sql"}, {4, "004_proxy_group_managed_metadata.sql"}} {
		body, readErr := migrationFiles.ReadFile("migrations/" + migration.name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := database.ExecContext(ctx, string(body)); execErr != nil {
			t.Fatal(execErr)
		}
		if _, execErr := database.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, 1700000000000)`, migration.version); execErr != nil {
			t.Fatal(execErr)
		}
	}
	for _, version := range []int{1, 2} {
		if _, err := database.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, 1700000000000)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO proxy_groups(
			id, resource_name, country_code, country_name, proxy_type, status,
			aimili_slot, vless_port, mixed_port, exit_ip, config_fingerprint,
			last_error_code, recovery_state, version, created_at, updated_at,
			last_checked_at, last_rotated_at, vless_inbound_id, mixed_inbound_id,
			reality_public_key, reality_short_id, reality_server_name
		) VALUES(
			'agw-jp-dc', 'agw-jp-dc', 'JP', '日本', 'datacenter', 'ready',
			0, 20000, 30000, '203.0.113.10', 'fingerprint', '', '', 3,
			1700000000000, 1700000001000, 1700000002000, 1700000003000,
			11, 12, 'public-key', 'short-id', 'example.test'
		);
		INSERT INTO proxy_operations(proxy_group_id, operation, phase, result, started_at, completed_at)
		VALUES('agw-jp-dc', 'enable', 'complete', 'success', 1700000000000, 1700000003000);
	`); err != nil {
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
	var candidateID, publicKey string
	var version, operationCount int
	if err := opened.db.QueryRowContext(ctx, `SELECT candidate_id, reality_public_key, version FROM proxy_groups WHERE id = 'agw-jp-dc'`).Scan(&candidateID, &publicKey, &version); err != nil {
		t.Fatal(err)
	}
	if candidateID != "" || publicKey != "public-key" || version != 3 {
		t.Fatalf("V1-B proxy group changed during migration: candidate=%q publicKey=%q version=%d", candidateID, publicKey, version)
	}
	if err := opened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_operations WHERE proxy_group_id = 'agw-jp-dc'`).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 1 {
		t.Fatalf("V1-B operation count = %d", operationCount)
	}
	if _, err := opened.db.ExecContext(ctx, `
		INSERT INTO proxy_groups(
			id, resource_name, country_code, proxy_type, candidate_id, status,
			aimili_slot, vless_port, mixed_port, version, created_at, updated_at
		) VALUES('agw-jp-dc-new', 'agw-jp-dc-new', 'JP', 'datacenter', 'candidate-new',
			'provisioning', 1, 20001, 30001, 1, 1700000010000, 1700000010000)
	`); err != nil {
		t.Fatalf("V1-C schema still rejects a second candidate in one classification: %v", err)
	}
}

func TestMigrationSixInitializesUnifiedAccountAndPreservesMixedRestriction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gateway.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for version, name := range []string{
		"001_initial.sql",
		"002_optional_totp.sql",
		"003_country_proxy.sql",
		"004_proxy_group_managed_metadata.sql",
		"005_online_egress_pool.sql",
	} {
		body, readErr := migrationFiles.ReadFile("migrations/" + name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := database.ExecContext(ctx, string(body)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
		if _, execErr := database.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, 1700000000000)`, version+1); execErr != nil {
			t.Fatal(execErr)
		}
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO mixed_source_cidrs(prefix, created_at) VALUES('198.51.100.0/24', 1700000000000)`); err != nil {
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
	state, err := opened.GetAccountSyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != AccountSyncResetRequired || state.ErrorCode != "" {
		t.Fatalf("initial account sync state = %#v", state)
	}
	policy, err := opened.GetMixedSourcePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Enabled || len(policy.CIDRs) != 1 || policy.CIDRs[0].String() != "198.51.100.0/24" {
		t.Fatalf("migrated mixed policy = %#v", policy)
	}
	for _, forbidden := range []string{"password", "plaintext"} {
		rows, queryErr := opened.db.QueryContext(ctx, `SELECT name FROM pragma_table_info('account_sync_state') WHERE lower(name) LIKE '%' || ? || '%'`, forbidden)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if rows.Next() {
			rows.Close()
			t.Fatalf("account sync schema contains forbidden %s column", forbidden)
		}
		rows.Close()
	}
}

func TestCommitUnifiedCredentialsIsAtomicAndRevokesSessions(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := database.CreateAdmin(ctx, Admin{Username: "owner", PasswordHash: []byte("old-hash"), CreatedAt: now, SecurityUpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	session := testSession("unified-account", now)
	if err := database.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := bytesOf(0x31, 32)
	usernameCiphertext, err := encryptCredential(UnifiedUsernamePurpose, []byte("renamed"), key)
	if err != nil {
		t.Fatal(err)
	}
	passwordCiphertext, err := encryptCredential(UnifiedPasswordPurpose, []byte("new-password-marker"), key)
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := now.Add(time.Minute)
	err = database.CommitUnifiedCredentialsAndRevokeSessions(ctx, UnifiedCredentialUpdate{
		ExpectedSecurityUpdatedAt: now,
		Username:                  "renamed",
		PasswordHash:              []byte("new-hash"),
		UsernameCiphertext:        usernameCiphertext,
		PasswordCiphertext:        passwordCiphertext,
		UsernameFingerprint:       "fingerprint",
		Status:                    AccountSyncSynced,
		UpdatedAt:                 updatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := database.GetAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if admin.Username != "renamed" || string(admin.PasswordHash) != "new-hash" || !admin.SecurityUpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated admin = %#v", admin)
	}
	if _, err := database.GetSession(ctx, session.TokenHash, updatedAt); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("old session remained usable: %v", err)
	}
	for purpose, want := range map[string]string{UnifiedUsernamePurpose: "renamed", UnifiedPasswordPurpose: "new-password-marker"} {
		got, getErr := database.GetCredential(ctx, purpose, key)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if string(got) != want {
			t.Fatalf("%s credential = %q", purpose, got)
		}
	}
	state, err := database.GetAccountSyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != AccountSyncSynced || state.UsernameFingerprint != "fingerprint" || !state.LastCheckedAt.Equal(updatedAt) {
		t.Fatalf("account sync state = %#v", state)
	}
}

func TestSealAndLoadUnifiedCredentialsUseIndependentPurposes(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	key := bytesOf(0x51, 32)
	usernameCiphertext, passwordCiphertext, err := SealUnifiedCredentials("owner", []byte("password-marker"), key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(usernameCiphertext, passwordCiphertext) || bytes.Contains(usernameCiphertext, []byte("owner")) || bytes.Contains(passwordCiphertext, []byte("password-marker")) {
		t.Fatal("unified credential sealing did not isolate plaintext or purposes")
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := database.CreateAdmin(ctx, Admin{Username: "owner", PasswordHash: []byte("hash"), CreatedAt: now, SecurityUpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.CommitUnifiedCredentialsAndRevokeSessions(ctx, UnifiedCredentialUpdate{
		ExpectedSecurityUpdatedAt: now,
		Username:                  "owner",
		PasswordHash:              []byte("new-hash"),
		UsernameCiphertext:        usernameCiphertext,
		PasswordCiphertext:        passwordCiphertext,
		UsernameFingerprint:       "fingerprint",
		Status:                    AccountSyncSynced,
		UpdatedAt:                 now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	credentials, err := database.LoadUnifiedCredentials(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Username != "owner" || string(credentials.Password) != "password-marker" {
		t.Fatalf("loaded unified credentials = %#v", credentials)
	}
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
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
