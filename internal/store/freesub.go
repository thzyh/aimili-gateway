package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

var ErrFreesubBackupNotFound = errors.New("freesub backup connection not found")

func (s *Store) GetFreesubBackup(ctx context.Context) (domain.FreesubBackupConnection, error) {
	return scanFreesubBackup(s.db.QueryRowContext(ctx, `
		SELECT id, candidate_id, country_code, protocol, candidate_ip, exit_ip,
			socks_port, public_port, xui_inbound_id, runtime_pid, status,
			repair_attempts, failure_fingerprint, last_error_code, candidate_config_ciphertext,
			version, created_at, updated_at, last_checked_at
		FROM freesub_backup_connections WHERE id = 'agw-freesub'`))
}

func (s *Store) PutFreesubBackup(ctx context.Context, connection domain.FreesubBackupConnection, expectedVersion int64, masterKey []byte) error {
	if err := connection.Validate(); err != nil || expectedVersion < 0 || len(connection.CandidateConfig) == 0 {
		return errors.New("invalid freesub backup connection")
	}
	ciphertext, err := encryptCredential("freesub-candidate", connection.CandidateConfig, masterKey)
	if err != nil {
		return err
	}
	now := connection.UpdatedAt.UTC().UnixMilli()
	if now == 0 {
		now = time.Now().UTC().UnixMilli()
	}
	if connection.CreatedAt.IsZero() {
		connection.CreatedAt = time.UnixMilli(now).UTC()
	}
	if expectedVersion == 0 {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO freesub_backup_connections(
				id, candidate_id, country_code, protocol, candidate_ip, exit_ip,
				socks_port, public_port, xui_inbound_id, runtime_pid, status,
				repair_attempts, failure_fingerprint, last_error_code, candidate_config_ciphertext,
				version, created_at, updated_at, last_checked_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			connection.ID, connection.CandidateID, connection.CountryCode, connection.Protocol,
			connection.CandidateIP, connection.ExitIP, connection.SocksPort, connection.PublicPort,
			connection.XUIInboundID, connection.RuntimePID, connection.Status, connection.RepairAttempts,
			connection.FailureFingerprint, connection.LastErrorCode, ciphertext,
			1, connection.CreatedAt.UTC().UnixMilli(), now, unixMillis(connection.LastCheckedAt))
		if err != nil {
			return fmt.Errorf("insert freesub backup connection: %w", err)
		}
		return nil
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE freesub_backup_connections SET
			candidate_id = ?, country_code = ?, protocol = ?, candidate_ip = ?, exit_ip = ?,
			socks_port = ?, public_port = ?, xui_inbound_id = ?, runtime_pid = ?, status = ?,
			repair_attempts = ?, failure_fingerprint = ?, last_error_code = ?, candidate_config_ciphertext = ?,
			version = version + 1, updated_at = ?, last_checked_at = ?
		WHERE id = 'agw-freesub' AND version = ?`,
		connection.CandidateID, connection.CountryCode, connection.Protocol, connection.CandidateIP,
		connection.ExitIP, connection.SocksPort, connection.PublicPort, connection.XUIInboundID,
		connection.RuntimePID, connection.Status, connection.RepairAttempts, connection.FailureFingerprint,
		connection.LastErrorCode, ciphertext, now, unixMillis(connection.LastCheckedAt), expectedVersion)
	if err != nil {
		return fmt.Errorf("update freesub backup connection: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read freesub backup update result: %w", err)
	}
	if changed != 1 {
		return ErrFreesubBackupChanged
	}
	return nil
}

func (s *Store) GetFreesubBackupConfig(ctx context.Context, masterKey []byte) (domain.FreesubBackupConnection, error) {
	connection, err := s.GetFreesubBackup(ctx)
	if err != nil {
		return domain.FreesubBackupConnection{}, err
	}
	plaintext, err := decryptCredential("freesub-candidate", connection.CandidateConfig, masterKey)
	if err != nil {
		return domain.FreesubBackupConnection{}, errors.New("decrypt freesub backup candidate")
	}
	connection.CandidateConfig = plaintext
	return connection, nil
}

var ErrFreesubBackupChanged = errors.New("freesub backup connection changed concurrently")

func scanFreesubBackup(row rowScanner) (domain.FreesubBackupConnection, error) {
	var c domain.FreesubBackupConnection
	var created, updated, checked int64
	err := row.Scan(&c.ID, &c.CandidateID, &c.CountryCode, &c.Protocol, &c.CandidateIP, &c.ExitIP,
		&c.SocksPort, &c.PublicPort, &c.XUIInboundID, &c.RuntimePID, &c.Status,
		&c.RepairAttempts, &c.FailureFingerprint, &c.LastErrorCode, &c.CandidateConfig,
		&c.Version, &created, &updated, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.FreesubBackupConnection{}, ErrFreesubBackupNotFound
	}
	if err != nil {
		return domain.FreesubBackupConnection{}, fmt.Errorf("scan freesub backup connection: %w", err)
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	c.UpdatedAt = time.UnixMilli(updated).UTC()
	c.LastCheckedAt = timeFromUnixMillis(checked)
	return c, nil
}
