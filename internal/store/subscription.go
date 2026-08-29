package store

import (
	"context"
	"errors"
	"time"
)

type GatewaySubscription struct {
	ResourceName   string
	ClientID       int64
	SubscriptionID string
	UpdatedAt      time.Time
}

func (s *Store) SaveGatewaySubscription(ctx context.Context, value GatewaySubscription) error {
	if value.ResourceName != "aimili-gateway-subscription" || value.ClientID < 0 || len(value.SubscriptionID) > 256 || value.UpdatedAt.IsZero() {
		return errors.New("invalid gateway subscription")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO gateway_subscription(id, resource_name, client_id, subscription_id, updated_at) VALUES(1, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET resource_name=excluded.resource_name, client_id=excluded.client_id, subscription_id=excluded.subscription_id, updated_at=excluded.updated_at`, value.ResourceName, value.ClientID, value.SubscriptionID, value.UpdatedAt.UTC().UnixMilli())
	return err
}

func (s *Store) GetGatewaySubscription(ctx context.Context) (GatewaySubscription, error) {
	var value GatewaySubscription
	var updatedAt int64
	if err := s.db.QueryRowContext(ctx, `SELECT resource_name, client_id, subscription_id, updated_at FROM gateway_subscription WHERE id=1`).Scan(&value.ResourceName, &value.ClientID, &value.SubscriptionID, &updatedAt); err != nil {
		return GatewaySubscription{}, err
	}
	if updatedAt > 0 {
		value.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	}
	return value, nil
}
