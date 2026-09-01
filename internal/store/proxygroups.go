package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

var (
	ErrProxyGroupExists   = errors.New("proxy group already exists")
	ErrProxyGroupNotFound = errors.New("proxy group not found")
	ErrProxyGroupChanged  = errors.New("proxy group changed concurrently")
)

func (s *Store) CreateProxyGroup(ctx context.Context, group domain.ProxyGroup) error {
	if err := validateProxyGroup(group); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proxy_groups(
			id, resource_name, country_code, country_name, proxy_type,
			candidate_id, candidate_ip, candidate_latency_ms, vless_latency_ms, socks_latency_ms, status,
			egress_source,
			aimili_slot, public_port, mixed_port, exit_ip, exit_ip_checked_at, config_fingerprint,
			public_inbound_id, mixed_inbound_id, reality_public_key, reality_short_id, reality_server_name, reality_mldsa65_verify,
			last_error_code, recovery_state, version, created_at, updated_at,
			last_checked_at, last_rotated_at, last_seen_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		group.ID, group.ResourceName, group.CountryCode, group.CountryName,
		group.ProxyType, group.CandidateID, group.CandidateIP, group.CandidateLatencyMS,
		group.VLESSLatencyMS, group.SOCKSLatencyMS, group.Status, normalizedEgressSource(group.EgressSource), group.AimiliSlot, group.PublicPort,
		group.MixedPort, group.ExitIP, group.ExitIPCheckedAt, group.ConfigFingerprint,
		group.PublicInboundID, group.MixedInboundID, group.RealityPublicKey,
		group.RealityShortID, group.RealityServerName, group.RealityMLDSA65Verify,
		group.LastErrorCode, group.RecoveryState, group.Version, group.CreatedAt.UTC().UnixMilli(),
		group.UpdatedAt.UTC().UnixMilli(), unixMillis(group.LastCheckedAt),
		unixMillis(group.LastRotatedAt), unixMillis(group.LastSeenAt),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "constraint") {
			return ErrProxyGroupExists
		}
		return fmt.Errorf("create proxy group: %w", err)
	}
	return nil
}

func (s *Store) GetProxyGroup(ctx context.Context, id string) (domain.ProxyGroup, error) {
	return scanProxyGroup(s.db.QueryRowContext(ctx, `
		SELECT id, resource_name, country_code, country_name, proxy_type,
			candidate_id, candidate_ip, candidate_latency_ms, vless_latency_ms, socks_latency_ms, status,
			egress_source,
			aimili_slot, public_port, mixed_port, exit_ip, exit_ip_checked_at, config_fingerprint,
			public_inbound_id, mixed_inbound_id, reality_public_key, reality_short_id, reality_server_name, reality_mldsa65_verify,
			last_error_code, recovery_state, version, created_at, updated_at,
			last_checked_at, last_rotated_at, last_seen_at
		FROM proxy_groups WHERE id = ?`, id))
}

func (s *Store) ListProxyGroups(ctx context.Context) ([]domain.ProxyGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, resource_name, country_code, country_name, proxy_type,
			candidate_id, candidate_ip, candidate_latency_ms, vless_latency_ms, socks_latency_ms, status,
			egress_source,
			aimili_slot, public_port, mixed_port, exit_ip, exit_ip_checked_at, config_fingerprint,
			public_inbound_id, mixed_inbound_id, reality_public_key, reality_short_id, reality_server_name, reality_mldsa65_verify,
			last_error_code, recovery_state, version, created_at, updated_at,
			last_checked_at, last_rotated_at, last_seen_at
		FROM proxy_groups ORDER BY country_code, proxy_type, candidate_latency_ms, id`)
	if err != nil {
		return nil, fmt.Errorf("list proxy groups: %w", err)
	}
	defer rows.Close()
	groups := make([]domain.ProxyGroup, 0)
	for rows.Next() {
		group, err := scanProxyGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list proxy groups: %w", err)
	}
	return groups, nil
}

func (s *Store) UpdateProxyGroup(ctx context.Context, group domain.ProxyGroup, expectedVersion int64) error {
	if err := validateProxyGroup(group); err != nil || expectedVersion < 1 {
		return errors.New("invalid proxy group update")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE proxy_groups SET
			resource_name = ?, country_code = ?, country_name = ?, proxy_type = ?, candidate_id = ?, candidate_ip = ?, candidate_latency_ms = ?, vless_latency_ms = ?, socks_latency_ms = ?,
			status = ?, aimili_slot = ?, public_port = ?, mixed_port = ?,
			egress_source = ?,
			exit_ip = ?, exit_ip_checked_at = ?, config_fingerprint = ?, public_inbound_id = ?, mixed_inbound_id = ?,
			reality_public_key = ?, reality_short_id = ?, reality_server_name = ?, reality_mldsa65_verify = ?, last_error_code = ?, recovery_state = ?,
			version = version + 1, updated_at = ?, last_checked_at = ?, last_rotated_at = ?, last_seen_at = ?
		WHERE id = ? AND version = ?`,
		group.ResourceName, group.CountryCode, group.CountryName, group.ProxyType, group.CandidateID, group.CandidateIP, group.CandidateLatencyMS, group.VLESSLatencyMS, group.SOCKSLatencyMS,
		group.Status, group.AimiliSlot, group.PublicPort, group.MixedPort, normalizedEgressSource(group.EgressSource),
		group.ExitIP, group.ExitIPCheckedAt, group.ConfigFingerprint, group.PublicInboundID,
		group.MixedInboundID, group.RealityPublicKey, group.RealityShortID, group.RealityServerName, group.RealityMLDSA65Verify, group.LastErrorCode,
		group.RecoveryState, group.UpdatedAt.UTC().UnixMilli(),
		unixMillis(group.LastCheckedAt), unixMillis(group.LastRotatedAt), unixMillis(group.LastSeenAt),
		group.ID, expectedVersion,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "constraint") {
			return ErrProxyGroupExists
		}
		return fmt.Errorf("update proxy group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read proxy group update result: %w", err)
	}
	if changed != 1 {
		return ErrProxyGroupChanged
	}
	return nil
}

func (s *Store) DeleteProxyGroup(ctx context.Context, id string) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin proxy group delete: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `DELETE FROM egress_operations WHERE egress_id = ?`, id); err != nil {
		return fmt.Errorf("delete proxy group operations: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM egress_protocol_modes WHERE egress_id = ?`, id); err != nil {
		return fmt.Errorf("delete proxy group protocol mode: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM proxy_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete proxy group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read proxy group delete result: %w", err)
	}
	if changed != 1 {
		return ErrProxyGroupNotFound
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit proxy group delete: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanProxyGroup(row rowScanner) (domain.ProxyGroup, error) {
	var group domain.ProxyGroup
	var createdAt, updatedAt, checkedAt, rotatedAt, seenAt int64
	err := row.Scan(
		&group.ID, &group.ResourceName, &group.CountryCode, &group.CountryName,
		&group.ProxyType, &group.CandidateID, &group.CandidateIP, &group.CandidateLatencyMS,
		&group.VLESSLatencyMS, &group.SOCKSLatencyMS, &group.Status, &group.EgressSource, &group.AimiliSlot, &group.PublicPort,
		&group.MixedPort, &group.ExitIP, &group.ExitIPCheckedAt, &group.ConfigFingerprint,
		&group.PublicInboundID, &group.MixedInboundID, &group.RealityPublicKey,
		&group.RealityShortID, &group.RealityServerName, &group.RealityMLDSA65Verify,
		&group.LastErrorCode, &group.RecoveryState, &group.Version,
		&createdAt, &updatedAt, &checkedAt, &rotatedAt, &seenAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProxyGroup{}, ErrProxyGroupNotFound
	}
	if err != nil {
		return domain.ProxyGroup{}, fmt.Errorf("scan proxy group: %w", err)
	}
	group.CreatedAt = time.UnixMilli(createdAt).UTC()
	group.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	group.LastCheckedAt = timeFromUnixMillis(checkedAt)
	group.LastRotatedAt = timeFromUnixMillis(rotatedAt)
	group.LastSeenAt = timeFromUnixMillis(seenAt)
	return group, nil
}

func validateProxyGroup(group domain.ProxyGroup) error {
	if !strings.HasPrefix(group.ID, "agw-") || !strings.HasPrefix(group.ResourceName, "agw-") ||
		len(group.CountryCode) != 2 || !group.ProxyType.Valid() || !group.Status.Valid() ||
		(group.EgressSource != "" && !group.EgressSource.Valid()) ||
		group.AimiliSlot < 0 || group.PublicPort < 1 || group.PublicPort > 65535 ||
		group.MixedPort < 1 || group.MixedPort > 65535 || group.Version < 1 ||
		len(group.CandidateID) > 256 || len(group.RealityMLDSA65Verify) > 4096 || strings.ContainsAny(group.RealityMLDSA65Verify, "\x00\r\n") || group.CandidateLatencyMS < 0 || group.VLESSLatencyMS < 0 || group.SOCKSLatencyMS < 0 || group.ExitIPCheckedAt < 0 ||
		group.CreatedAt.IsZero() || group.UpdatedAt.IsZero() {
		return errors.New("invalid proxy group")
	}
	return nil
}

func normalizedEgressSource(source domain.EgressSource) domain.EgressSource {
	if source == "" {
		return domain.EgressSourceSlot
	}
	return source
}

func unixMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func timeFromUnixMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}
