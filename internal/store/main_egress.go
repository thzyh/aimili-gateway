package store

import (
	"context"
	"errors"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

type MainEgress struct {
	ResourceName       string
	CountryCode        string
	CountryName        string
	ProxyType          domain.ProxyType
	ExitIP             string
	VLESSInboundID     int64
	MixedInboundID     int64
	VLESSPort          int
	MixedPort          int
	Enabled            bool
	CandidateLatencyMS int
	VLESSLatencyMS     int
	SOCKSLatencyMS     int
	LastCheckedAt      time.Time
	LastErrorCode      string
	UpdatedAt          time.Time
}

func (s *Store) SaveMainEgress(ctx context.Context, value MainEgress) error {
	if value.ResourceName != "agw-main" || len(value.CountryCode) != 2 || !value.ProxyType.Valid() || value.VLESSInboundID < 1 || value.MixedInboundID < 1 || value.VLESSPort != 8443 || value.MixedPort < 1 || value.MixedPort > 65535 || value.UpdatedAt.IsZero() {
		return errors.New("invalid main egress")
	}
	if value.CandidateLatencyMS < 0 || value.VLESSLatencyMS < 0 || value.SOCKSLatencyMS < 0 || len(value.LastErrorCode) > 64 {
		return errors.New("invalid main egress probe")
	}
	lastChecked := int64(0)
	if !value.LastCheckedAt.IsZero() {
		lastChecked = value.LastCheckedAt.UTC().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO main_egress(id, resource_name, country_code, country_name, proxy_type, exit_ip, vless_inbound_id, mixed_inbound_id, vless_port, mixed_port, enabled, candidate_latency_ms, vless_latency_ms, socks_latency_ms, last_checked_at, last_error_code, updated_at)
		VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET resource_name=excluded.resource_name, country_code=excluded.country_code, country_name=excluded.country_name, proxy_type=excluded.proxy_type, exit_ip=excluded.exit_ip, vless_inbound_id=excluded.vless_inbound_id, mixed_inbound_id=excluded.mixed_inbound_id, vless_port=excluded.vless_port, mixed_port=excluded.mixed_port, enabled=excluded.enabled, candidate_latency_ms=excluded.candidate_latency_ms, vless_latency_ms=excluded.vless_latency_ms, socks_latency_ms=excluded.socks_latency_ms, last_checked_at=excluded.last_checked_at, last_error_code=excluded.last_error_code, updated_at=excluded.updated_at`,
		value.ResourceName, value.CountryCode, value.CountryName, value.ProxyType, value.ExitIP, value.VLESSInboundID, value.MixedInboundID, value.VLESSPort, value.MixedPort, boolInt(value.Enabled), value.CandidateLatencyMS, value.VLESSLatencyMS, value.SOCKSLatencyMS, lastChecked, value.LastErrorCode, value.UpdatedAt.UTC().UnixMilli())
	return err
}

func (s *Store) GetMainEgress(ctx context.Context) (MainEgress, error) {
	var value MainEgress
	var enabled, updatedAt, lastChecked int
	err := s.db.QueryRowContext(ctx, `SELECT resource_name, country_code, country_name, proxy_type, exit_ip, vless_inbound_id, mixed_inbound_id, vless_port, mixed_port, enabled, candidate_latency_ms, vless_latency_ms, socks_latency_ms, last_checked_at, last_error_code, updated_at FROM main_egress WHERE id=1`).Scan(
		&value.ResourceName, &value.CountryCode, &value.CountryName, &value.ProxyType, &value.ExitIP, &value.VLESSInboundID, &value.MixedInboundID, &value.VLESSPort, &value.MixedPort, &enabled, &value.CandidateLatencyMS, &value.VLESSLatencyMS, &value.SOCKSLatencyMS, &lastChecked, &value.LastErrorCode, &updatedAt)
	if err != nil {
		return MainEgress{}, err
	}
	value.Enabled = enabled != 0
	if lastChecked > 0 {
		value.LastCheckedAt = time.UnixMilli(int64(lastChecked)).UTC()
	}
	if updatedAt > 0 {
		value.UpdatedAt = time.UnixMilli(int64(updatedAt)).UTC()
	}
	return value, nil
}
