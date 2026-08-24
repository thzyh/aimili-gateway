package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")

type Session struct {
	ID                int64
	TokenHash         [32]byte
	CSRFTokenHash     [32]byte
	CreatedAt         time.Time
	LastActiveAt      time.Time
	ExpiresAt         time.Time
	ReauthenticatedAt *time.Time
	RevokedAt         *time.Time
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	var reauthenticatedAt any
	if session.ReauthenticatedAt != nil {
		reauthenticatedAt = session.ReauthenticatedAt.UTC().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(token_hash, csrf_token_hash, created_at, last_active_at, expires_at, reauthenticated_at)
		VALUES(?, ?, ?, ?, ?, ?)`,
		session.TokenHash[:],
		session.CSRFTokenHash[:],
		session.CreatedAt.UTC().UnixMilli(),
		session.LastActiveAt.UTC().UnixMilli(),
		session.ExpiresAt.UTC().UnixMilli(),
		reauthenticatedAt,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, tokenHash [32]byte, now time.Time) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, token_hash, csrf_token_hash, created_at, last_active_at, expires_at, reauthenticated_at, revoked_at
		FROM sessions
		WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?`, tokenHash[:], now.UTC().UnixMilli())
	return scanSession(row)
}

func (s *Store) TouchSession(ctx context.Context, id int64, now time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_active_at = ? WHERE id = ? AND revoked_at IS NULL`,
		now.UTC().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return requireChanged(result)
}

func (s *Store) ReauthenticateSession(ctx context.Context, id int64, now time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET reauthenticated_at = ? WHERE id = ? AND revoked_at IS NULL AND expires_at > ?`,
		now.UTC().UnixMilli(), id, now.UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("reauthenticate session: %w", err)
	}
	return requireChanged(result)
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash [32]byte) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		time.Now().UTC().UnixMilli(), tokenHash[:])
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return requireChanged(result)
}

func (s *Store) RevokeAllSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE revoked_at IS NULL`, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("revoke all sessions: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (Session, error) {
	var session Session
	var tokenHash []byte
	var csrfTokenHash []byte
	var createdAt int64
	var lastActiveAt int64
	var expiresAt int64
	var reauthenticatedAt sql.NullInt64
	var revokedAt sql.NullInt64
	err := row.Scan(
		&session.ID,
		&tokenHash,
		&csrfTokenHash,
		&createdAt,
		&lastActiveAt,
		&expiresAt,
		&reauthenticatedAt,
		&revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	if len(tokenHash) != len(session.TokenHash) || len(csrfTokenHash) != len(session.CSRFTokenHash) {
		return Session{}, errors.New("stored session hash has invalid length")
	}
	copy(session.TokenHash[:], tokenHash)
	copy(session.CSRFTokenHash[:], csrfTokenHash)
	session.CreatedAt = time.UnixMilli(createdAt).UTC()
	session.LastActiveAt = time.UnixMilli(lastActiveAt).UTC()
	session.ExpiresAt = time.UnixMilli(expiresAt).UTC()
	if reauthenticatedAt.Valid {
		value := time.UnixMilli(reauthenticatedAt.Int64).UTC()
		session.ReauthenticatedAt = &value
	}
	if revokedAt.Valid {
		value := time.UnixMilli(revokedAt.Int64).UTC()
		session.RevokedAt = &value
	}
	return session, nil
}

func requireChanged(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if count == 0 {
		return ErrSessionNotFound
	}
	return nil
}
