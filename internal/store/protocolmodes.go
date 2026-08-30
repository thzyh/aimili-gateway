package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

var (
	ErrEgressProtocolNotFound = errors.New("egress protocol mode not found")
	ErrEgressProtocolChanged  = errors.New("egress protocol mode changed concurrently")
)

var (
	safeOperationID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	safeErrorCode   = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
)

func (s *Store) CreateEgressProtocolMode(ctx context.Context, value domain.EgressProtocolMode) error {
	if err := validateEgressProtocolMode(value); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO egress_protocol_modes(
			egress_id, active_mode, desired_mode, state, last_operation_id,
			last_request_hash, last_error_code, version, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.EgressID, value.ActiveMode, value.DesiredMode, value.State,
		value.LastOperationID, value.LastRequestHash, value.LastErrorCode,
		value.Version, value.UpdatedAt.UTC().UnixMilli(),
	)
	return err
}

func (s *Store) GetEgressProtocolMode(ctx context.Context, egressID string) (domain.EgressProtocolMode, error) {
	var value domain.EgressProtocolMode
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT egress_id, active_mode, desired_mode, state, last_operation_id,
			last_request_hash, last_error_code, version, updated_at
		FROM egress_protocol_modes WHERE egress_id = ?`, egressID).Scan(
		&value.EgressID, &value.ActiveMode, &value.DesiredMode, &value.State,
		&value.LastOperationID, &value.LastRequestHash, &value.LastErrorCode,
		&value.Version, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EgressProtocolMode{}, ErrEgressProtocolNotFound
	}
	if err != nil {
		return domain.EgressProtocolMode{}, err
	}
	value.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return value, nil
}

func (s *Store) ListEgressProtocolModes(ctx context.Context) ([]domain.EgressProtocolMode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT egress_id, active_mode, desired_mode, state, last_operation_id,
			last_request_hash, last_error_code, version, updated_at
		FROM egress_protocol_modes ORDER BY egress_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.EgressProtocolMode, 0)
	for rows.Next() {
		var value domain.EgressProtocolMode
		var updatedAt int64
		if err := rows.Scan(
			&value.EgressID, &value.ActiveMode, &value.DesiredMode, &value.State,
			&value.LastOperationID, &value.LastRequestHash, &value.LastErrorCode,
			&value.Version, &updatedAt,
		); err != nil {
			return nil, err
		}
		value.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) UpdateEgressProtocolMode(ctx context.Context, value domain.EgressProtocolMode, expectedVersion int64) error {
	if err := validateEgressProtocolMode(value); err != nil || expectedVersion < 1 {
		return errors.New("invalid egress protocol update")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE egress_protocol_modes SET
			active_mode = ?, desired_mode = ?, state = ?, last_operation_id = ?,
			last_request_hash = ?, last_error_code = ?, version = version + 1, updated_at = ?
		WHERE egress_id = ? AND version = ?`,
		value.ActiveMode, value.DesiredMode, value.State, value.LastOperationID,
		value.LastRequestHash, value.LastErrorCode, value.UpdatedAt.UTC().UnixMilli(),
		value.EgressID, expectedVersion,
	)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrEgressProtocolChanged
	}
	return nil
}

func validateEgressProtocolMode(value domain.EgressProtocolMode) error {
	if !strings.HasPrefix(value.EgressID, "agw-") ||
		!value.ActiveMode.Valid() || !value.DesiredMode.Valid() || !value.State.Valid() ||
		len(value.LastOperationID) > 128 ||
		(len(value.LastRequestHash) != 0 && len(value.LastRequestHash) != 64) ||
		len(value.LastErrorCode) > 64 || value.Version < 1 || value.UpdatedAt.IsZero() {
		return errors.New("invalid egress protocol mode")
	}
	return nil
}

type EgressOperation struct {
	OperationID   string
	EgressID      string
	Kind          string
	Phase         string
	RequestHash   string
	TransactionID string
	ErrorCode     string
	StartedAt     time.Time
	CompletedAt   time.Time
}

func (s *Store) CreateEgressOperation(ctx context.Context, value EgressOperation) error {
	if err := validateEgressOperation(value); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO egress_operations(
			operation_id, egress_id, kind, phase, request_hash, transaction_id,
			error_code, started_at, completed_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.OperationID, value.EgressID, value.Kind, value.Phase, value.RequestHash,
		value.TransactionID, value.ErrorCode, value.StartedAt.UTC().UnixMilli(),
		unixMillis(value.CompletedAt),
	)
	return err
}

func (s *Store) GetEgressOperationByRequestHash(ctx context.Context, egressID, kind, requestHash string) (EgressOperation, error) {
	var value EgressOperation
	var startedAt, completedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT operation_id, egress_id, kind, phase, request_hash, transaction_id,
			error_code, started_at, completed_at
		FROM egress_operations
		WHERE egress_id = ? AND kind = ? AND request_hash = ?`,
		egressID, kind, requestHash,
	).Scan(
		&value.OperationID, &value.EgressID, &value.Kind, &value.Phase,
		&value.RequestHash, &value.TransactionID, &value.ErrorCode,
		&startedAt, &completedAt,
	)
	if err != nil {
		return EgressOperation{}, err
	}
	value.StartedAt = time.UnixMilli(startedAt).UTC()
	value.CompletedAt = timeFromUnixMillis(completedAt)
	return value, nil
}

func (s *Store) CompleteEgressOperation(ctx context.Context, operationID string, completedAt time.Time) error {
	if !safeOperationID.MatchString(operationID) || completedAt.IsZero() {
		return errors.New("invalid egress operation completion")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE egress_operations
		SET phase = 'completed', completed_at = ?
		WHERE operation_id = ? AND phase = 'started'`, completedAt.UTC().UnixMilli(), operationID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("egress operation completion conflict")
	}
	return nil
}

func (s *Store) FailEgressOperation(ctx context.Context, operationID, errorCode string, completedAt time.Time) error {
	if !safeOperationID.MatchString(operationID) || !safeErrorCode.MatchString(errorCode) || completedAt.IsZero() {
		return errors.New("invalid egress operation failure")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE egress_operations
		SET phase = 'failed', error_code = ?, completed_at = ?
		WHERE operation_id = ? AND phase = 'started'`,
		errorCode, completedAt.UTC().UnixMilli(), operationID,
	)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("egress operation failure conflict")
	}
	return nil
}

func validateEgressOperation(value EgressOperation) error {
	if !safeOperationID.MatchString(value.OperationID) || !strings.HasPrefix(value.EgressID, "agw-") ||
		(value.Kind != "main_assign" && value.Kind != "protocol_switch") ||
		len(value.Phase) < 1 || len(value.Phase) > 64 || len(value.RequestHash) != 64 ||
		len(value.TransactionID) > 128 || len(value.ErrorCode) > 64 || value.StartedAt.IsZero() {
		return errors.New("invalid egress operation")
	}
	return nil
}
