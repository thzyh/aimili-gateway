package store

import (
	"context"
	"errors"
	"time"
)

type AggregateConfig struct {
	ResourceName   string
	VLESSInboundID int64
	VLESSPort      int
	Enabled        bool
	UpdatedAt      time.Time
}

func (s *Store) GetAggregateConfig(ctx context.Context) (AggregateConfig, error) {
	var result AggregateConfig
	var enabled int
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT resource_name, vless_inbound_id, vless_port, enabled, updated_at FROM aggregate_config WHERE id = 1`).Scan(&result.ResourceName, &result.VLESSInboundID, &result.VLESSPort, &enabled, &updated)
	if err != nil {
		return AggregateConfig{}, err
	}
	result.Enabled = enabled == 1
	if updated != 0 {
		result.UpdatedAt = time.UnixMilli(updated).UTC()
	}
	return result, nil
}

func (s *Store) SaveAggregateConfig(ctx context.Context, value AggregateConfig) error {
	if value.ResourceName == "" || value.VLESSPort < 0 || value.VLESSPort > 65535 || value.VLESSInboundID < 0 {
		return errors.New("invalid aggregate config")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE aggregate_config SET resource_name = ?, vless_inbound_id = ?, vless_port = ?, enabled = ?, updated_at = ? WHERE id = 1`, value.ResourceName, value.VLESSInboundID, value.VLESSPort, boolInt(value.Enabled), value.UpdatedAt.UTC().UnixMilli())
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
