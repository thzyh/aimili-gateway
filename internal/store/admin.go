package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrAdminExists   = errors.New("administrator already exists")
	ErrAdminNotFound = errors.New("administrator not found")
	ErrAdminChanged  = errors.New("administrator changed concurrently")
)

type Admin struct {
	Username             string
	PasswordHash         []byte
	TOTPEnabled          bool
	TOTPSecretCiphertext []byte
	CreatedAt            time.Time
	SecurityUpdatedAt    time.Time
}

type AdminSecurityUpdate struct {
	ExpectedSecurityUpdatedAt time.Time
	Username                  string
	PasswordHash              []byte
	TOTPEnabled               bool
	TOTPSecretCiphertext      []byte
	Action                    string
	UpdatedAt                 time.Time
}

func (s *Store) CreateAdmin(ctx context.Context, admin Admin) error {
	if err := validateAdminSecurity(admin.Username, admin.PasswordHash, admin.TOTPEnabled, admin.TOTPSecretCiphertext); err != nil {
		return errors.New("administrator fields are required")
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin administrator transaction: %w", err)
	}
	defer transaction.Rollback()
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO admin(id, username, password_hash, totp_enabled, totp_secret_ciphertext, created_at, security_updated_at)
		VALUES(1, ?, ?, ?, ?, ?, ?)`,
		admin.Username,
		admin.PasswordHash,
		admin.TOTPEnabled,
		admin.TOTPSecretCiphertext,
		admin.CreatedAt.UTC().UnixMilli(),
		admin.SecurityUpdatedAt.UTC().UnixMilli(),
	)
	if err != nil {
		_ = transaction.Rollback()
		var exists int
		if queryErr := s.db.QueryRowContext(ctx, `SELECT 1 FROM admin WHERE id = 1`).Scan(&exists); queryErr == nil {
			return ErrAdminExists
		}
		return fmt.Errorf("create administrator: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit administrator: %w", err)
	}
	return nil
}

func (s *Store) GetAdmin(ctx context.Context) (Admin, error) {
	var admin Admin
	var createdAt int64
	var securityUpdatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT username, password_hash, totp_enabled, totp_secret_ciphertext, created_at, security_updated_at
		FROM admin WHERE id = 1`).Scan(
		&admin.Username,
		&admin.PasswordHash,
		&admin.TOTPEnabled,
		&admin.TOTPSecretCiphertext,
		&createdAt,
		&securityUpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, ErrAdminNotFound
	}
	if err != nil {
		return Admin{}, fmt.Errorf("get administrator: %w", err)
	}
	admin.CreatedAt = time.UnixMilli(createdAt).UTC()
	admin.SecurityUpdatedAt = time.UnixMilli(securityUpdatedAt).UTC()
	return admin, nil
}

func (s *Store) UpdateAdminSecurityAndRevokeSessions(ctx context.Context, update AdminSecurityUpdate) error {
	if err := validateAdminSecurity(update.Username, update.PasswordHash, update.TOTPEnabled, update.TOTPSecretCiphertext); err != nil {
		return err
	}
	if update.ExpectedSecurityUpdatedAt.IsZero() || update.UpdatedAt.IsZero() || update.Action == "" || len(update.Action) > 128 {
		return errors.New("administrator update fields are required")
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin administrator security update: %w", err)
	}
	defer transaction.Rollback()

	result, err := transaction.ExecContext(ctx, `
		UPDATE admin
		SET username = ?, password_hash = ?, totp_enabled = ?, totp_secret_ciphertext = ?, security_updated_at = ?
		WHERE id = 1 AND security_updated_at = ?`,
		update.Username,
		update.PasswordHash,
		update.TOTPEnabled,
		update.TOTPSecretCiphertext,
		update.UpdatedAt.UTC().UnixMilli(),
		update.ExpectedSecurityUpdatedAt.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("update administrator security: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read administrator update result: %w", err)
	}
	if changed != 1 {
		return ErrAdminChanged
	}
	if _, err := transaction.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE revoked_at IS NULL`, update.UpdatedAt.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("revoke sessions after administrator update: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO audit_events(session_id, action, resource_type, resource_fingerprint, result, error_code, created_at)
		VALUES(NULL, ?, 'gateway_account', 'local-admin', 'success', '', ?)`,
		update.Action, update.UpdatedAt.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("audit administrator update: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit administrator security update: %w", err)
	}
	return nil
}

func validateAdminSecurity(username string, passwordHash []byte, totpEnabled bool, totpSecretCiphertext []byte) error {
	if username == "" || len(passwordHash) == 0 {
		return errors.New("administrator fields are required")
	}
	if totpEnabled && len(totpSecretCiphertext) == 0 {
		return errors.New("TOTP secret is required when TOTP is enabled")
	}
	if !totpEnabled && len(totpSecretCiphertext) != 0 {
		return errors.New("TOTP secret must be empty when TOTP is disabled")
	}
	return nil
}
