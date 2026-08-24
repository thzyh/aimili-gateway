package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type AuditResult string

const (
	AuditSuccess AuditResult = "success"
	AuditFailure AuditResult = "failure"
	AuditDenied  AuditResult = "denied"
)

type AuditEvent struct {
	SessionID           *int64
	Action              string
	ResourceType        string
	ResourceFingerprint string
	Result              AuditResult
	ErrorCode           string
	CreatedAt           time.Time
}

func (s *Store) AppendAudit(ctx context.Context, event AuditEvent) error {
	if event.Action == "" || event.ResourceType == "" || event.ResourceFingerprint == "" {
		return errors.New("audit fields are required")
	}
	if event.Result != AuditSuccess && event.Result != AuditFailure && event.Result != AuditDenied {
		return errors.New("invalid audit result")
	}

	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit transaction: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO audit_events(session_id, action, resource_type, resource_fingerprint, result, error_code, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		event.SessionID,
		event.Action,
		event.ResourceType,
		event.ResourceFingerprint,
		string(event.Result),
		event.ErrorCode,
		event.CreatedAt.UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit audit event: %w", err)
	}
	return nil
}
