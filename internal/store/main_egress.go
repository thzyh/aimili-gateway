package store

import (
	"context"
	"errors"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

type MainEgress struct {
	ResourceName   string
	CountryCode    string
	CountryName    string
	ProxyType      domain.ProxyType
	ExitIP         string
	VLESSInboundID int64
	MixedInboundID int64
	VLESSPort      int
	MixedPort      int
	Enabled        bool
	UpdatedAt      time.Time
}

func (s *Store) SaveMainEgress(ctx context.Context, value MainEgress) error {
	if value.ResourceName != "agw-main" || len(value.CountryCode) != 2 || !value.ProxyType.Valid() || value.VLESSInboundID < 1 || value.MixedInboundID < 1 || value.VLESSPort != 8443 || value.MixedPort < 1 || value.MixedPort > 65535 || value.UpdatedAt.IsZero() {
		return errors.New("invalid main egress")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO main_egress(id, resource_name, country_code, country_name, proxy_type, exit_ip, vless_inbound_id, mixed_inbound_id, vless_port, mixed_port, enabled, updated_at)
		VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET resource_name=excluded.resource_name, country_code=excluded.country_code, country_name=excluded.country_name, proxy_type=excluded.proxy_type, exit_ip=excluded.exit_ip, vless_inbound_id=excluded.vless_inbound_id, mixed_inbound_id=excluded.mixed_inbound_id, vless_port=excluded.vless_port, mixed_port=excluded.mixed_port, enabled=excluded.enabled, updated_at=excluded.updated_at`,
		value.ResourceName, value.CountryCode, value.CountryName, value.ProxyType, value.ExitIP, value.VLESSInboundID, value.MixedInboundID, value.VLESSPort, value.MixedPort, boolInt(value.Enabled), value.UpdatedAt.UTC().UnixMilli())
	return err
}
