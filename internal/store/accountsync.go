package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	UnifiedUsernamePurpose = "unified-username"
	UnifiedPasswordPurpose = "unified-password"
)

type AccountSyncStatus string

const (
	AccountSyncResetRequired  AccountSyncStatus = "reset_required"
	AccountSyncSynced         AccountSyncStatus = "synced"
	AccountSyncChecking       AccountSyncStatus = "checking"
	AccountSyncRepairRequired AccountSyncStatus = "repair_required"
	AccountSyncIncompatible   AccountSyncStatus = "incompatible"
)

type AccountSyncState struct {
	Status              AccountSyncStatus
	UsernameFingerprint string
	LastCheckedAt       time.Time
	ErrorCode           string
}

type UnifiedCredentialUpdate struct {
	ExpectedSecurityUpdatedAt time.Time
	Username                  string
	PasswordHash              []byte
	UsernameCiphertext        []byte
	PasswordCiphertext        []byte
	UsernameFingerprint       string
	Status                    AccountSyncStatus
	UpdatedAt                 time.Time
}

func (s *Store) GetAccountSyncState(ctx context.Context) (AccountSyncState, error) {
	var state AccountSyncState
	var lastCheckedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT status, username_fingerprint, last_checked_at, error_code
		FROM account_sync_state WHERE id = 1`).Scan(
		&state.Status,
		&state.UsernameFingerprint,
		&lastCheckedAt,
		&state.ErrorCode,
	)
	if err != nil {
		return AccountSyncState{}, fmt.Errorf("get account sync state: %w", err)
	}
	if lastCheckedAt > 0 {
		state.LastCheckedAt = time.UnixMilli(lastCheckedAt).UTC()
	}
	return state, nil
}

func (s *Store) SetAccountSyncState(ctx context.Context, state AccountSyncState) error {
	if !validAccountSyncStatus(state.Status) || len(state.UsernameFingerprint) > 128 || len(state.ErrorCode) > 64 {
		return errors.New("invalid account sync state")
	}
	lastCheckedAt := int64(0)
	if !state.LastCheckedAt.IsZero() {
		lastCheckedAt = state.LastCheckedAt.UTC().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE account_sync_state
		SET status = ?, username_fingerprint = ?, last_checked_at = ?, error_code = ?
		WHERE id = 1`, state.Status, state.UsernameFingerprint, lastCheckedAt, state.ErrorCode)
	if err != nil {
		return fmt.Errorf("set account sync state: %w", err)
	}
	return nil
}

func (s *Store) CommitUnifiedCredentialsAndRevokeSessions(ctx context.Context, update UnifiedCredentialUpdate) error {
	if update.ExpectedSecurityUpdatedAt.IsZero() || update.UpdatedAt.IsZero() ||
		update.Username == "" || len(update.PasswordHash) == 0 ||
		len(update.UsernameCiphertext) == 0 || len(update.PasswordCiphertext) == 0 ||
		len(update.UsernameFingerprint) == 0 || len(update.UsernameFingerprint) > 128 ||
		!validAccountSyncStatus(update.Status) {
		return errors.New("unified credential update fields are required")
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unified credential update: %w", err)
	}
	defer transaction.Rollback()

	result, err := transaction.ExecContext(ctx, `
		UPDATE admin
		SET username = ?, password_hash = ?, security_updated_at = ?
		WHERE id = 1 AND security_updated_at = ?`,
		update.Username,
		update.PasswordHash,
		update.UpdatedAt.UTC().UnixMilli(),
		update.ExpectedSecurityUpdatedAt.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("update administrator for unified credentials: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read unified administrator update result: %w", err)
	}
	if changed != 1 {
		return ErrAdminChanged
	}

	for _, credential := range []struct {
		purpose    string
		ciphertext []byte
	}{
		{UnifiedUsernamePurpose, update.UsernameCiphertext},
		{UnifiedPasswordPurpose, update.PasswordCiphertext},
	} {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO encrypted_credentials(purpose, ciphertext, key_version, created_at, updated_at)
			VALUES(?, ?, 1, ?, ?)
			ON CONFLICT(purpose) DO UPDATE SET ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
			credential.purpose,
			credential.ciphertext,
			update.UpdatedAt.UTC().UnixMilli(),
			update.UpdatedAt.UTC().UnixMilli(),
		); err != nil {
			return fmt.Errorf("store unified credential: %w", err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE account_sync_state
		SET status = ?, username_fingerprint = ?, last_checked_at = ?, error_code = ''
		WHERE id = 1`,
		update.Status,
		update.UsernameFingerprint,
		update.UpdatedAt.UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("update unified account sync state: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE revoked_at IS NULL`,
		update.UpdatedAt.UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("revoke sessions after unified credential update: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO audit_events(session_id, action, resource_type, resource_fingerprint, result, error_code, created_at)
		VALUES(NULL, 'account.unified_credentials_updated', 'gateway_account', 'local-admin', 'success', '', ?)`,
		update.UpdatedAt.UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("audit unified credential update: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO account_operations(operation, phase, result, started_at, completed_at)
		VALUES('change', 'complete', 'success', ?, ?)`,
		update.UpdatedAt.UTC().UnixMilli(),
		update.UpdatedAt.UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("record unified credential operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit unified credential update: %w", err)
	}
	return nil
}

func validAccountSyncStatus(status AccountSyncStatus) bool {
	switch status {
	case AccountSyncResetRequired, AccountSyncSynced, AccountSyncChecking, AccountSyncRepairRequired, AccountSyncIncompatible:
		return true
	default:
		return false
	}
}
