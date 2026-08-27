package orchestrator

import (
	"context"
	"net"
	"net/netip"
	"sort"

	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type mixedPolicyUpdate struct {
	group      domain.ProxyGroup
	managed    xui.ManagedGroup
	desired    xui.DesiredGroup
	oldDesired xui.DesiredGroup
	updated    xui.ManagedGroup
}

func (o *Orchestrator) MixedPolicy(ctx context.Context) (store.MixedSourcePolicy, error) {
	policy, err := o.store.GetMixedSourcePolicy(ctx)
	if err != nil {
		return store.MixedSourcePolicy{}, &Error{Code: "storage_failed"}
	}
	return policy, nil
}

func (o *Orchestrator) RepairManaged(ctx context.Context) error {
	policy, err := o.store.GetMixedSourcePolicy(ctx)
	if err != nil {
		return &Error{Code: "storage_failed"}
	}
	return o.SetMixedPolicy(ctx, policy)
}

func (o *Orchestrator) SetMixedPolicy(ctx context.Context, requested store.MixedSourcePolicy) error {
	desired, err := canonicalMixedPolicy(requested)
	if err != nil {
		return err
	}
	unlock := o.locks.lock("all")
	defer unlock()

	oldPolicy, err := o.store.GetMixedSourcePolicy(ctx)
	if err != nil {
		return &Error{Code: "storage_failed"}
	}
	credentials, err := o.runtimeCredentials(ctx)
	if err != nil {
		return err
	}
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return &Error{Code: "storage_failed"}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	updates := make([]mixedPolicyUpdate, 0, len(groups))
	for _, group := range groups {
		if group.VLESSInboundID <= 0 || group.MixedInboundID <= 0 {
			continue
		}
		slot, checkErr := o.aimili.CheckSlot(ctx, group.AimiliSlot)
		if checkErr != nil || !slot.EgressOK || slot.Port < 1 || net.ParseIP(slot.ExitIP) == nil {
			return &Error{Code: "egress_unavailable"}
		}
		managed := managedFromGroup(group)
		updates = append(updates, mixedPolicyUpdate{
			group: group, managed: managed,
			desired:    o.desiredGroup(group, slot.Port, credentials, desired),
			oldDesired: o.desiredGroup(group, slot.Port, credentials, oldPolicy),
		})
	}

	now := o.config.Now().UTC()
	desired.ApplyStatus = store.MixedPolicyApplying
	desired.UpdatedAt = now
	if err := o.store.ReplaceMixedSourcePolicy(ctx, desired); err != nil {
		return &Error{Code: "storage_failed"}
	}

	applied := make([]mixedPolicyUpdate, 0, len(updates))
	for _, update := range updates {
		updated, updateErr := o.xui.UpdateManagedGroup(ctx, update.desired, update.managed)
		if updateErr == nil {
			update.updated = updated
			applied = append(applied, update)
			_, updateErr = o.validateSOCKS(ctx, update.group, credentials)
		}
		if updateErr != nil {
			return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, nil)
		}
	}

	saved := make([]mixedPolicyUpdate, 0, len(applied))
	for _, update := range applied {
		changed := update.group
		changed.ConfigFingerprint = update.updated.Fingerprint
		changed.RealityPublicKey = update.updated.PublicKey
		changed.RealityShortID = update.updated.ShortID
		changed.RealityServerName = update.updated.ServerName
		changed.UpdatedAt = now
		if err := o.save(ctx, &changed); err != nil {
			return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, saved)
		}
		saved = append(saved, update)
	}
	desired.ApplyStatus = store.MixedPolicyApplied
	if err := o.store.ReplaceMixedSourcePolicy(ctx, desired); err != nil {
		return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, saved)
	}
	return nil
}

func (o *Orchestrator) failMixedPolicyUpdate(ctx context.Context, oldPolicy, desired store.MixedSourcePolicy, applied, saved []mixedPolicyUpdate) error {
	rollbackFailed := false
	for index := len(applied) - 1; index >= 0; index-- {
		update := applied[index]
		if _, err := o.xui.UpdateManagedGroup(ctx, update.oldDesired, update.updated); err != nil {
			rollbackFailed = true
			continue
		}
		if _, err := o.validateSOCKS(ctx, update.group, mustRuntimeCredentials(update.oldDesired)); err != nil {
			rollbackFailed = true
		}
	}
	for index := len(saved) - 1; index >= 0; index-- {
		update := saved[index]
		restored := update.group
		restored.Version++
		restored.UpdatedAt = o.config.Now().UTC()
		if err := o.save(ctx, &restored); err != nil {
			rollbackFailed = true
		}
	}
	if rollbackFailed {
		desired.ApplyStatus = store.MixedPolicyRepairRequired
		desired.UpdatedAt = o.config.Now().UTC()
		_ = o.store.ReplaceMixedSourcePolicy(ctx, desired)
		return &Error{Code: "repair_required"}
	}
	oldPolicy.ApplyStatus = store.MixedPolicyFailed
	oldPolicy.UpdatedAt = o.config.Now().UTC()
	if err := o.store.ReplaceMixedSourcePolicy(ctx, oldPolicy); err != nil {
		return &Error{Code: "repair_required"}
	}
	return &Error{Code: "mixed_policy_apply_failed"}
}

func (o *Orchestrator) desiredGroup(group domain.ProxyGroup, socksPort int, credentials runtimeCredentials, policy store.MixedSourcePolicy) xui.DesiredGroup {
	return xui.DesiredGroup{
		ResourceName: group.ResourceName, SOCKSPort: socksPort, VLESSPort: group.VLESSPort, MixedPort: group.MixedPort,
		VLESSClientID: string(credentials.vlessID), MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword),
		MixedSourceRestrictionEnabled: policy.Enabled, MixedSourceCIDRs: prefixStrings(policy.CIDRs),
		RealityTarget: "127.0.0.1:443", RealityServerName: o.config.PublicHost,
	}
}

func mustRuntimeCredentials(desired xui.DesiredGroup) runtimeCredentials {
	return runtimeCredentials{
		vlessID:       []byte(desired.VLESSClientID),
		mixedUsername: []byte(desired.MixedUsername),
		mixedPassword: []byte(desired.MixedPassword),
	}
}

func canonicalMixedPolicy(policy store.MixedSourcePolicy) (store.MixedSourcePolicy, error) {
	seen := make(map[string]struct{}, len(policy.CIDRs))
	canonical := make([]netip.Prefix, 0, len(policy.CIDRs))
	for _, prefix := range policy.CIDRs {
		if !prefix.IsValid() || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return store.MixedSourcePolicy{}, &Error{Code: "invalid_cidr"}
		}
		if _, exists := seen[prefix.String()]; exists {
			continue
		}
		seen[prefix.String()] = struct{}{}
		canonical = append(canonical, prefix)
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].String() < canonical[j].String() })
	if policy.Enabled && len(canonical) == 0 {
		return store.MixedSourcePolicy{}, &Error{Code: "mixed_cidr_required"}
	}
	policy.CIDRs = canonical
	return policy, nil
}
