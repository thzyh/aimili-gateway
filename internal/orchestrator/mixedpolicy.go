package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func (o *Orchestrator) RotateMixedCredentials(ctx context.Context) (time.Time, error) {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	unlock := o.locks.lock("all")
	defer unlock()

	persistence, ok := o.store.(mixedCredentialStore)
	if !ok {
		return time.Time{}, &Error{Code: "not_configured"}
	}
	policy, err := o.store.GetMixedSourcePolicy(ctx)
	if err != nil {
		return time.Time{}, &Error{Code: "storage_failed"}
	}
	oldCredentials, err := o.runtimeCredentials(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer clearRuntimeCredentials(&oldCredentials)
	newCredentials, err := generateMixedCredentials()
	if err != nil {
		return time.Time{}, &Error{Code: "operation_failed"}
	}
	newCredentials.vlessID = append([]byte(nil), oldCredentials.vlessID...)
	defer clearRuntimeCredentials(&newCredentials)

	slots, err := o.aimili.ListSlots(ctx)
	if err != nil {
		return time.Time{}, operationError(err)
	}
	slotsByNumber := make(map[int]aimili.Slot, len(slots))
	for _, slot := range slots {
		slotsByNumber[slot.Number] = slot
	}
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return time.Time{}, &Error{Code: "storage_failed"}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	updates := make([]mixedPolicyUpdate, 0, len(groups))
	for _, group := range groups {
		if group.PublicInboundID <= 0 || group.MixedInboundID <= 0 {
			continue
		}
		slot, exists := slotsByNumber[group.AimiliSlot]
		if !exists || slot.Port < 1 {
			return time.Time{}, &Error{Code: "not_ready"}
		}
		updates = append(updates, mixedPolicyUpdate{
			group: group, original: group, managed: managedFromGroup(group),
			desired:    o.desiredGroup(group, slot.Port, newCredentials, policy),
			oldDesired: o.desiredGroup(group, slot.Port, oldCredentials, policy),
		})
	}

	var mainUpdate *mixedPolicyMainUpdate
	if mainStore, ok := o.store.(mainEgressStore); ok {
		main, mainErr := mainStore.GetMainEgress(ctx)
		if mainErr != nil && !errors.Is(mainErr, store.ErrProxyGroupNotFound) {
			return time.Time{}, &Error{Code: "storage_failed"}
		}
		if mainErr == nil && main.Enabled {
			mainUpdate = &mixedPolicyMainUpdate{
				group: mainEgressGroup(main), desired: o.desiredLegacyMain(newCredentials, policy), oldDesired: o.desiredLegacyMain(oldCredentials, policy),
			}
		}
	}
	if len(updates) == 0 && mainUpdate == nil {
		return time.Time{}, &Error{Code: "not_configured"}
	}

	applied := make([]mixedPolicyUpdate, 0, len(updates))
	for index := range updates {
		updates[index].updated = updates[index].managed
		updated, updateErr := o.xui.UpdateManagedMixedPolicy(ctx, updates[index].desired, updates[index].managed)
		if updateErr != nil {
			log.Printf("mixed credential rotation failed: stage=update_managed id=%s slot=%d code=%s", updates[index].group.ID, updates[index].group.AimiliSlot, errorCode(updateErr))
			return time.Time{}, o.rollbackMixedCredentialRotation(ctx, append(applied, updates[index]), nil, nil)
		}
		updates[index].updated = updated
		updates[index].oldDesired.ResourceName = updated.ResourceName
		applied = append(applied, updates[index])
	}
	if mainUpdate != nil {
		if updateErr := o.xui.UpdateLegacyMainMixedPolicy(ctx, mainUpdate.desired); updateErr != nil {
			log.Printf("mixed credential rotation failed: stage=update_main id=%s code=%s", mainUpdate.group.ID, errorCode(updateErr))
			return time.Time{}, o.rollbackMixedCredentialRotation(ctx, applied, nil, mainUpdate)
		}
	}

	for _, update := range applied {
		slot := slotsByNumber[update.group.AimiliSlot]
		if update.group.Status == domain.ProxyGroupReady && slot.EgressOK {
			if _, validateErr := o.validateSOCKS(ctx, update.group, newCredentials); validateErr != nil {
				log.Printf("mixed credential rotation failed: stage=validate_managed id=%s slot=%d code=%s", update.group.ID, update.group.AimiliSlot, errorCode(validateErr))
				return time.Time{}, o.rollbackMixedCredentialRotation(ctx, applied, nil, mainUpdate)
			}
		}
	}
	if mainUpdate != nil {
		mainStatus, statusErr := o.aimili.MainStatus(ctx)
		if statusErr != nil {
			log.Printf("mixed credential rotation failed: stage=main_status id=%s code=%s", mainUpdate.group.ID, errorCode(statusErr))
			return time.Time{}, o.rollbackMixedCredentialRotation(ctx, applied, nil, mainUpdate)
		}
		if mainStatus.Active && mainStatus.EgressOK {
			mainUpdate.group.ExitIP = mainStatus.ExitIP
			if _, validateErr := o.validateSOCKS(ctx, mainUpdate.group, newCredentials); validateErr != nil {
				log.Printf("mixed credential rotation failed: stage=validate_main id=%s code=%s", mainUpdate.group.ID, errorCode(validateErr))
				return time.Time{}, o.rollbackMixedCredentialRotation(ctx, applied, nil, mainUpdate)
			}
		}
	}

	saved := make([]mixedPolicyUpdate, 0, len(applied))
	for _, update := range applied {
		changed := update.group
		changed.ResourceName = update.updated.ResourceName
		changed.ConfigFingerprint = update.updated.Fingerprint
		changed.MixedInboundID = update.updated.MixedInboundID
		changed.UpdatedAt = o.config.Now().UTC()
		if err := o.save(ctx, &changed); err != nil {
			log.Printf("mixed credential rotation failed: stage=save_managed id=%s slot=%d code=%s", update.group.ID, update.group.AimiliSlot, errorCode(err))
			return time.Time{}, o.rollbackMixedCredentialRotation(ctx, applied, saved, mainUpdate)
		}
		saved = append(saved, update)
	}
	if err := persistence.ReplaceMixedCredentials(ctx, newCredentials.mixedUsername, newCredentials.mixedPassword, o.masterKey); err != nil {
		log.Printf("mixed credential rotation failed: stage=save_credentials code=%s", errorCode(err))
		return time.Time{}, o.rollbackMixedCredentialRotation(ctx, applied, saved, mainUpdate)
	}
	return o.config.Now().UTC(), nil
}

func (o *Orchestrator) rollbackMixedCredentialRotation(ctx context.Context, applied, saved []mixedPolicyUpdate, mainUpdate *mixedPolicyMainUpdate) error {
	rollbackFailed := false
	if mainUpdate != nil {
		if err := o.xui.UpdateLegacyMainMixedPolicy(ctx, mainUpdate.oldDesired); err != nil {
			rollbackFailed = true
		}
	}
	for index := len(applied) - 1; index >= 0; index-- {
		update := applied[index]
		if _, err := o.xui.UpdateManagedMixedPolicy(ctx, update.oldDesired, update.updated); err != nil {
			rollbackFailed = true
		}
	}
	for index := len(saved) - 1; index >= 0; index-- {
		restored := saved[index].original
		restored.Version++
		restored.UpdatedAt = o.config.Now().UTC()
		if err := o.save(ctx, &restored); err != nil {
			rollbackFailed = true
		}
	}
	if rollbackFailed {
		return &Error{Code: "repair_required"}
	}
	return &Error{Code: "mixed_credentials_apply_failed"}
}

func generateMixedCredentials() (runtimeCredentials, error) {
	username, err := randomCredentialHex(8)
	if err != nil {
		return runtimeCredentials{}, err
	}
	password, err := randomCredentialHex(24)
	if err != nil {
		return runtimeCredentials{}, err
	}
	return runtimeCredentials{mixedUsername: []byte("agw-" + username), mixedPassword: []byte(password)}, nil
}

func randomCredentialHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func clearRuntimeCredentials(credentials *runtimeCredentials) {
	clear(credentials.vlessID)
	clear(credentials.mixedUsername)
	clear(credentials.mixedPassword)
}

type mixedPolicyUpdate struct {
	group      domain.ProxyGroup
	original   domain.ProxyGroup
	managed    xui.ManagedGroup
	desired    xui.DesiredGroup
	oldDesired xui.DesiredGroup
	updated    xui.ManagedGroup
}

type mixedPolicyMainUpdate struct {
	group      domain.ProxyGroup
	desired    xui.LegacyMainDesired
	oldDesired xui.LegacyMainDesired
}

func (o *Orchestrator) MixedPolicy(ctx context.Context) (store.MixedSourcePolicy, error) {
	policy, err := o.store.GetMixedSourcePolicy(ctx)
	if err != nil {
		return store.MixedSourcePolicy{}, &Error{Code: "storage_failed"}
	}
	return policy, nil
}

func (o *Orchestrator) RepairManaged(ctx context.Context) error {
	if mainStore, ok := o.store.(mainEgressStore); ok {
		main, err := mainStore.GetMainEgress(ctx)
		if err != nil && !errors.Is(err, store.ErrProxyGroupNotFound) {
			return &Error{Code: "storage_failed"}
		}
		status, statusErr := o.aimili.MainStatus(ctx)
		if statusErr != nil {
			return operationError(statusErr)
		}
		if status.Active && status.EgressOK && (errors.Is(err, store.ErrProxyGroupNotFound) || main.CandidateID != status.CandidateID || main.ExitIP != status.ExitIP) {
			if _, checkErr := o.CheckMain(ctx); checkErr != nil {
				return checkErr
			}
		}
	}
	policy, err := o.store.GetMixedSourcePolicy(ctx)
	if err != nil {
		return &Error{Code: "storage_failed"}
	}
	return o.setMixedPolicy(ctx, policy, true)
}

func (o *Orchestrator) SetMixedPolicy(ctx context.Context, requested store.MixedSourcePolicy) error {
	return o.setMixedPolicy(ctx, requested, false)
}

func (o *Orchestrator) setMixedPolicy(ctx context.Context, requested store.MixedSourcePolicy, repairPublic bool) error {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
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
	var mainUpdate *mixedPolicyMainUpdate
	if mainStore, ok := o.store.(mainEgressStore); ok {
		main, mainErr := mainStore.GetMainEgress(ctx)
		if mainErr != nil && !errors.Is(mainErr, store.ErrProxyGroupNotFound) {
			return &Error{Code: "storage_failed"}
		}
		if mainErr == nil && main.Enabled {
			mainUpdate = &mixedPolicyMainUpdate{
				group: mainEgressGroup(main), desired: o.desiredLegacyMain(credentials, desired), oldDesired: o.desiredLegacyMain(credentials, oldPolicy),
			}
		}
	}
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return &Error{Code: "storage_failed"}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	updates := make([]mixedPolicyUpdate, 0, len(groups))
	for _, group := range groups {
		if group.PublicInboundID <= 0 || group.MixedInboundID <= 0 {
			continue
		}
		slot, checkErr := o.aimili.CheckSlot(ctx, group.AimiliSlot)
		if checkErr != nil || !slot.EgressOK || slot.Port < 1 || net.ParseIP(slot.ExitIP) == nil {
			return &Error{Code: "egress_unavailable"}
		}
		original := group
		applySlotSnapshot(&group, slot)
		managed := managedFromGroup(group)
		if repairPublic {
			persistence, ok := o.store.(protocolModeStore)
			if !ok {
				return &Error{Code: "not_configured"}
			}
			state, stateErr := persistence.GetEgressProtocolMode(ctx, group.ID)
			if stateErr != nil || state.State != domain.ProtocolReady || !state.ActiveMode.Valid() {
				return &Error{Code: "not_ready"}
			}
			managed, err = o.xui.RepairManagedPublic(ctx, o.desiredGroup(group, slot.Port, credentials, desired), managed, state.ActiveMode)
			if err != nil {
				return operationError(err)
			}
		}
		updates = append(updates, mixedPolicyUpdate{
			group: group, original: original, managed: managed,
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
		updated, updateErr := o.xui.UpdateManagedMixedPolicy(ctx, update.desired, update.managed)
		if updateErr == nil {
			update.updated = updated
			update.oldDesired.ResourceName = updated.ResourceName
			applied = append(applied, update)
			_, updateErr = o.validateSOCKS(ctx, update.group, credentials)
			if updateErr != nil {
				log.Printf("mixed policy apply failed: stage=validate_managed id=%s code=%s", update.group.ID, errorCode(updateErr))
			}
		} else {
			log.Printf("mixed policy apply failed: stage=update_managed id=%s code=%s", update.group.ID, errorCode(updateErr))
		}
		if updateErr != nil {
			return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, nil, nil)
		}
	}
	if mainUpdate != nil {
		if updateErr := o.xui.UpdateLegacyMainMixedPolicy(ctx, mainUpdate.desired); updateErr != nil {
			log.Printf("mixed policy apply failed: stage=update_main code=%s", errorCode(updateErr))
			return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, nil, mainUpdate)
		}
		if updateErr := o.validateMainPolicySOCKS(ctx, mainUpdate.group, credentials); updateErr != nil {
			log.Printf("mixed policy apply failed: stage=validate_main code=%s", errorCode(updateErr))
			return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, nil, mainUpdate)
		}
	}

	saved := make([]mixedPolicyUpdate, 0, len(applied))
	for _, update := range applied {
		changed := update.group
		changed.ResourceName = update.updated.ResourceName
		changed.ConfigFingerprint = update.updated.Fingerprint
		changed.PublicInboundID = update.updated.VLESSInboundID
		changed.MixedInboundID = update.updated.MixedInboundID
		changed.RealityPublicKey = update.updated.PublicKey
		changed.RealityShortID = update.updated.ShortID
		changed.RealityServerName = update.updated.ServerName
		changed.RealityMLDSA65Verify = update.updated.MLDSA65Verify
		changed.UpdatedAt = now
		if err := o.save(ctx, &changed); err != nil {
			log.Printf("mixed policy apply failed: stage=save_managed id=%s code=%s", update.group.ID, errorCode(err))
			return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, saved, mainUpdate)
		}
		saved = append(saved, update)
	}
	desired.ApplyStatus = store.MixedPolicyApplied
	if err := o.store.ReplaceMixedSourcePolicy(ctx, desired); err != nil {
		log.Printf("mixed policy apply failed: stage=save_policy code=%s", errorCode(err))
		return o.failMixedPolicyUpdate(ctx, oldPolicy, desired, applied, saved, mainUpdate)
	}
	return nil
}

func (o *Orchestrator) failMixedPolicyUpdate(ctx context.Context, oldPolicy, desired store.MixedSourcePolicy, applied, saved []mixedPolicyUpdate, mainUpdate *mixedPolicyMainUpdate) error {
	rollbackFailed := false
	if mainUpdate != nil {
		if err := o.xui.UpdateLegacyMainMixedPolicy(ctx, mainUpdate.oldDesired); err != nil {
			log.Printf("mixed policy rollback failed: stage=restore_main code=%s", errorCode(err))
			rollbackFailed = true
		} else if err := o.validateMainPolicySOCKS(ctx, mainUpdate.group, runtimeCredentials{mixedUsername: []byte(mainUpdate.oldDesired.MixedUsername), mixedPassword: []byte(mainUpdate.oldDesired.MixedPassword)}); err != nil {
			log.Printf("mixed policy rollback failed: stage=validate_main code=%s", errorCode(err))
			rollbackFailed = true
		}
	}
	for index := len(applied) - 1; index >= 0; index-- {
		update := applied[index]
		if _, err := o.xui.UpdateManagedMixedPolicy(ctx, update.oldDesired, update.updated); err != nil {
			log.Printf("mixed policy rollback failed: stage=restore_managed id=%s code=%s", update.group.ID, errorCode(err))
			rollbackFailed = true
			continue
		}
		if _, err := o.validateSOCKS(ctx, update.group, mustRuntimeCredentials(update.oldDesired)); err != nil {
			log.Printf("mixed policy rollback failed: stage=validate_managed id=%s code=%s", update.group.ID, errorCode(err))
			rollbackFailed = true
		}
	}
	for index := len(saved) - 1; index >= 0; index-- {
		update := saved[index]
		restored := update.original
		restored.Version++
		restored.UpdatedAt = o.config.Now().UTC()
		if err := o.save(ctx, &restored); err != nil {
			log.Printf("mixed policy rollback failed: stage=restore_group id=%s code=%s", update.group.ID, errorCode(err))
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

// A healthy main node may rotate after its last Gateway check. Retry only an
// exit-IP mismatch against the current, healthy AimiliVPN main assignment.
func (o *Orchestrator) validateMainPolicySOCKS(ctx context.Context, group domain.ProxyGroup, credentials runtimeCredentials) error {
	_, err := o.validateSOCKS(ctx, group, credentials)
	if errorCode(err) != "egress_mismatch" {
		return err
	}
	current, statusErr := o.aimili.MainStatus(ctx)
	if statusErr != nil || !current.Active || !current.EgressOK || net.ParseIP(current.ExitIP) == nil || current.ExitIP == group.ExitIP {
		return err
	}
	group.ExitIP = current.ExitIP
	_, err = o.validateSOCKS(ctx, group, credentials)
	return err
}

func (o *Orchestrator) desiredGroup(group domain.ProxyGroup, socksPort int, credentials runtimeCredentials, policy store.MixedSourcePolicy) xui.DesiredGroup {
	return xui.DesiredGroup{
		ResourceName: group.ResourceName, SOCKSPort: socksPort, VLESSPort: group.PublicPort, MixedPort: group.MixedPort,
		VLESSClientID: string(credentials.vlessID), MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword),
		MixedSourceRestrictionEnabled: policy.Enabled, MixedSourceCIDRs: prefixStrings(policy.CIDRs),
		RealityTarget: "127.0.0.1:443", RealityServerName: o.config.RealityServerName,
	}
}

func (o *Orchestrator) desiredLegacyMain(credentials runtimeCredentials, policy store.MixedSourcePolicy) xui.LegacyMainDesired {
	return xui.LegacyMainDesired{
		VLESSPort: 8443, MixedPort: o.config.MainMixedPort, SOCKSPort: 7928,
		VLESSClientID: string(credentials.vlessID), MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword),
		MixedSourceRestrictionEnabled: policy.Enabled, MixedSourceCIDRs: prefixStrings(policy.CIDRs),
		RealityTarget: "127.0.0.1:443", RealityServerName: o.config.RealityServerName,
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
