package store

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

type MixedPolicyApplyStatus string

const (
	MixedPolicyPending        MixedPolicyApplyStatus = "pending"
	MixedPolicyApplying       MixedPolicyApplyStatus = "applying"
	MixedPolicyApplied        MixedPolicyApplyStatus = "applied"
	MixedPolicyFailed         MixedPolicyApplyStatus = "failed"
	MixedPolicyRepairRequired MixedPolicyApplyStatus = "repair_required"
)

type MixedSourcePolicy struct {
	Enabled     bool
	CIDRs       []netip.Prefix
	ApplyStatus MixedPolicyApplyStatus
	UpdatedAt   time.Time
}

func (s *Store) GetMixedSourcePolicy(ctx context.Context) (MixedSourcePolicy, error) {
	var policy MixedSourcePolicy
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, apply_status, updated_at FROM mixed_source_policy WHERE id = 1`).Scan(
		&policy.Enabled,
		&policy.ApplyStatus,
		&updatedAt,
	)
	if err != nil {
		return MixedSourcePolicy{}, fmt.Errorf("get mixed source policy: %w", err)
	}
	policy.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	policy.CIDRs, err = s.ListMixedCIDRs(ctx)
	if err != nil {
		return MixedSourcePolicy{}, err
	}
	return policy, nil
}

func (s *Store) ReplaceMixedSourcePolicy(ctx context.Context, policy MixedSourcePolicy) error {
	canonical, err := canonicalMixedCIDRs(policy.CIDRs)
	if err != nil {
		return err
	}
	if policy.Enabled && len(canonical) == 0 {
		return errors.New("enabled mixed source policy requires at least one CIDR")
	}
	if !validMixedPolicyApplyStatus(policy.ApplyStatus) || policy.UpdatedAt.IsZero() {
		return errors.New("mixed source policy status and update time are required")
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mixed source policy update: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `DELETE FROM mixed_source_cidrs`); err != nil {
		return fmt.Errorf("clear mixed source policy CIDRs: %w", err)
	}
	for _, prefix := range canonical {
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO mixed_source_cidrs(prefix, created_at) VALUES(?, ?)`,
			prefix,
			policy.UpdatedAt.UTC().UnixMilli(),
		); err != nil {
			return fmt.Errorf("insert mixed source policy CIDR: %w", err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE mixed_source_policy SET enabled = ?, apply_status = ?, updated_at = ? WHERE id = 1`,
		policy.Enabled,
		policy.ApplyStatus,
		policy.UpdatedAt.UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("update mixed source policy: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit mixed source policy update: %w", err)
	}
	return nil
}

func canonicalMixedCIDRs(prefixes []netip.Prefix) ([]string, error) {
	canonical := make([]string, 0, len(prefixes))
	seen := make(map[string]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		if !prefix.IsValid() || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return nil, errors.New("mixed source CIDR must be a canonical non-global prefix")
		}
		text := prefix.String()
		if _, exists := seen[text]; exists {
			continue
		}
		seen[text] = struct{}{}
		canonical = append(canonical, text)
	}
	sort.Strings(canonical)
	return canonical, nil
}

func validMixedPolicyApplyStatus(status MixedPolicyApplyStatus) bool {
	switch status {
	case MixedPolicyPending, MixedPolicyApplying, MixedPolicyApplied, MixedPolicyFailed, MixedPolicyRepairRequired:
		return true
	default:
		return false
	}
}
