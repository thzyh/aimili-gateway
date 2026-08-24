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
)

type Admin struct {
	Username             string
	PasswordHash         []byte
	TOTPSecretCiphertext []byte
	CreatedAt            time.Time
	SecurityUpdatedAt    time.Time
}

func (s *Store) CreateAdmin(ctx context.Context, admin Admin) error {
	if admin.Username == "" || len(admin.PasswordHash) == 0 || len(admin.TOTPSecretCiphertext) == 0 {
		return errors.New("administrator fields are required")
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin administrator transaction: %w", err)
	}
	defer transaction.Rollback()
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO admin(id, username, password_hash, totp_secret_ciphertext, created_at, security_updated_at)
		VALUES(1, ?, ?, ?, ?, ?)`,
		admin.Username,
		admin.PasswordHash,
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
		SELECT username, password_hash, totp_secret_ciphertext, created_at, security_updated_at
		FROM admin WHERE id = 1`).Scan(
		&admin.Username,
		&admin.PasswordHash,
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
