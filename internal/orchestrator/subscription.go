package orchestrator

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

const legacyAggregateVLESSPort = 21000

type LegacyAggregateCleanup struct {
	Removed   bool      `json:"removed"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (o *Orchestrator) Subscription(ctx context.Context) (SubscriptionResult, error) {
	manager, ok := o.xui.(subscriptionXUIClient)
	if !ok {
		return SubscriptionResult{}, &Error{Code: "not_configured"}
	}
	persistence, ok := o.store.(subscriptionStore)
	if !ok {
		return SubscriptionResult{}, &Error{Code: "not_configured"}
	}
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return SubscriptionResult{}, &Error{Code: "storage_failed"}
	}
	snapshot, err := manager.Snapshot(ctx)
	if err != nil {
		return SubscriptionResult{}, operationError(err)
	}
	ids := make([]int64, 0, len(groups)+1)
	for _, inbound := range snapshot.Inbounds {
		if inbound.ID > 0 && inbound.Tag == "aimili-reality" && inbound.Protocol == "vless" && inbound.Port == 8443 && inbound.Remark == "Aimili Reality" {
			ids = append(ids, inbound.ID)
		}
	}
	for _, group := range groups {
		if group.Status == domain.ProxyGroupReady && group.PublicInboundID > 0 {
			ids = append(ids, group.PublicInboundID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return SubscriptionResult{}, &Error{Code: "not_ready"}
	}
	credentials, err := o.runtimeCredentials(ctx)
	if err != nil {
		return SubscriptionResult{}, err
	}
	subscription, err := manager.EnsureSubscriptionClient(ctx, xui.SubscriptionDesired{
		ClientEmail: "aimili-gateway-subscription", ClientUUID: string(credentials.vlessID), InboundIDs: ids,
	})
	if err != nil {
		return SubscriptionResult{}, operationError(err)
	}
	if err := xui.ValidateSubscriptionCoverage(subscription.InboundIDs, ids); err != nil {
		return SubscriptionResult{}, operationError(err)
	}
	relative, err := manager.SubscriptionURL(ctx, subscription)
	if err != nil {
		return SubscriptionResult{}, operationError(err)
	}
	reference, err := url.Parse(relative)
	if err != nil || reference.IsAbs() || !strings.HasPrefix(reference.Path, "/") || reference.RawQuery != "" || reference.Fragment != "" {
		return SubscriptionResult{}, &Error{Code: "invalid_response"}
	}
	updatedAt := o.config.Now().UTC()
	if err := persistence.SaveGatewaySubscription(ctx, store.GatewaySubscription{ResourceName: subscription.ResourceName, ClientID: subscription.ClientID, SubscriptionID: subscription.SubscriptionID, UpdatedAt: updatedAt}); err != nil {
		return SubscriptionResult{}, &Error{Code: "storage_failed"}
	}
	return SubscriptionResult{URL: "https://" + o.config.PublicHost + reference.EscapedPath(), InboundCount: len(ids), UpdatedAt: updatedAt}, nil
}

// CleanupLegacyAggregate removes the retired single-entry balancer only after
// the replacement subscription has been reconciled and its inbound coverage
// has been checked again. Database backups remain a deployment responsibility.
func (o *Orchestrator) CleanupLegacyAggregate(ctx context.Context) (LegacyAggregateCleanup, error) {
	manager, ok := o.xui.(legacyAggregateCleanupXUIClient)
	if !ok {
		return LegacyAggregateCleanup{}, &Error{Code: "not_configured"}
	}
	unlock := o.locks.lock("all")
	defer unlock()
	aggregate, err := o.store.GetAggregateConfig(ctx)
	if err != nil {
		return LegacyAggregateCleanup{}, &Error{Code: "storage_failed"}
	}
	if !aggregate.Enabled {
		return LegacyAggregateCleanup{Removed: false, UpdatedAt: aggregate.UpdatedAt}, nil
	}
	if aggregate.ResourceName != "agw-aggregate-vless" || aggregate.VLESSInboundID <= 0 || aggregate.VLESSPort != legacyAggregateVLESSPort {
		return LegacyAggregateCleanup{}, &Error{Code: "ownership_conflict"}
	}
	if _, err := o.Subscription(ctx); err != nil {
		return LegacyAggregateCleanup{}, err
	}
	managed := xui.ManagedAggregate{
		ResourceName:    aggregate.ResourceName,
		VLESSInboundID:  aggregate.VLESSInboundID,
		VLESSInboundTag: aggregate.ResourceName + "-vless",
		VLESSPort:       aggregate.VLESSPort,
	}
	if err := manager.DeleteManagedAggregate(ctx, managed); err != nil {
		var adapterError *xui.AdapterError
		if errors.As(err, &adapterError) && adapterError.Code == "partial_delete" {
			return LegacyAggregateCleanup{}, &Error{Code: "repair_required"}
		}
		return LegacyAggregateCleanup{}, operationError(err)
	}
	aggregate.Enabled = false
	aggregate.UpdatedAt = o.config.Now().UTC()
	if err := o.store.SaveAggregateConfig(ctx, aggregate); err != nil {
		return LegacyAggregateCleanup{}, &Error{Code: "repair_required"}
	}
	return LegacyAggregateCleanup{Removed: true, UpdatedAt: aggregate.UpdatedAt}, nil
}

func (o *Orchestrator) ReplaceCandidate(ctx context.Context, candidateID, targetGroupID string) (domain.ProxyGroup, error) {
	if targetGroupID == "agw-main" || strings.TrimSpace(candidateID) == "" {
		return domain.ProxyGroup{}, &Error{Code: "invalid_request"}
	}
	assigner, ok := o.aimili.(assignAimiliClient)
	if !ok {
		return domain.ProxyGroup{}, &Error{Code: "not_configured"}
	}
	unlock := o.locks.lock(targetGroupID)
	defer unlock()
	group, err := o.store.GetProxyGroup(ctx, targetGroupID)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	if group.Status != domain.ProxyGroupReady || group.AimiliSlot < 0 {
		return domain.ProxyGroup{}, &Error{Code: "conflict"}
	}
	slots, err := o.aimili.ListSlots(ctx)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	var previousSlot aimili.Slot
	for _, slot := range slots {
		if slot.Number == group.AimiliSlot {
			previousSlot = slot
			break
		}
	}
	if strings.TrimSpace(previousSlot.NodeID) == "" || len(strings.TrimSpace(previousSlot.Country)) != 2 || !domain.ProxyType(strings.ToLower(strings.TrimSpace(previousSlot.ProxyType))).Valid() {
		return domain.ProxyGroup{}, &Error{Code: "conflict"}
	}
	candidates, err := o.aimili.Candidates(ctx)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	var candidate *aimili.Candidate
	for index := range candidates {
		current := &candidates[index]
		identity, identityErr := domain.NewProxyGroupIdentity(current.CountryCode, domain.ProxyType(current.ProxyType), current.ID)
		if identityErr == nil && (identity.ID == strings.TrimSpace(candidateID) || strings.TrimSpace(current.ID) == strings.TrimSpace(candidateID)) && current.ProbeStatus == "available" && domain.ProxyType(current.ProxyType).Valid() {
			candidate = current
			break
		}
	}
	if candidate == nil {
		return domain.ProxyGroup{}, &Error{Code: "not_found"}
	}
	assigned, err := assigner.AssignSlotNode(ctx, group.AimiliSlot, aimili.AssignSlotRequest{CandidateID: candidate.ID, Country: candidate.CountryCode, ProxyType: candidate.ProxyType})
	if err == nil {
		assigned, err = o.waitForSlot(ctx, group.AimiliSlot)
	}
	if err != nil || !assigned.EgressOK || net.ParseIP(assigned.ExitIP) == nil {
		if err == nil {
			err = &Error{Code: "egress_unavailable"}
		}
		return o.rollbackCandidateReplacement(ctx, group, previousSlot, assigner, err)
	}
	group.CandidateID = candidate.ID
	group.CandidateIP = candidate.IP
	group.CandidateLatencyMS = candidate.LatencyMS
	group.CountryCode = strings.ToUpper(candidate.CountryCode)
	group.CountryName = candidate.CountryName
	group.ProxyType = domain.ProxyType(candidate.ProxyType)
	group.ExitIP = assigned.ExitIP
	_, credentials, inputErr := o.runtimeInputs(ctx)
	if inputErr == nil {
		var socksResult, vlessResult validator.Result
		socksResult, inputErr = o.validateSOCKS(ctx, group, credentials)
		if inputErr == nil {
			vlessResult, inputErr = o.validateVLESS(ctx, group, credentials)
		}
		if inputErr == nil {
			group.SOCKSLatencyMS = durationMillis(socksResult.Latency)
			group.VLESSLatencyMS = durationMillis(vlessResult.Latency)
		}
	}
	if inputErr != nil {
		return o.rollbackCandidateReplacement(ctx, group, previousSlot, assigner, inputErr)
	}
	group.LastCheckedAt = o.config.Now().UTC()
	group.LastSeenAt = group.LastCheckedAt
	group.UpdatedAt = group.LastCheckedAt
	group.Status = domain.ProxyGroupReady
	group.LastErrorCode = ""
	if err := o.save(ctx, &group); err != nil {
		return domain.ProxyGroup{}, err
	}
	return group, nil
}

func (o *Orchestrator) rollbackCandidateReplacement(ctx context.Context, group domain.ProxyGroup, previous aimili.Slot, assigner assignAimiliClient, cause error) (domain.ProxyGroup, error) {
	restored, rollbackErr := assigner.AssignSlotNode(ctx, group.AimiliSlot, aimili.AssignSlotRequest{
		CandidateID: strings.TrimSpace(previous.NodeID),
		Country:     strings.ToUpper(strings.TrimSpace(previous.Country)),
		ProxyType:   strings.ToLower(strings.TrimSpace(previous.ProxyType)),
	})
	if rollbackErr == nil {
		restored, rollbackErr = o.waitForSlot(ctx, group.AimiliSlot)
	}
	if rollbackErr != nil || !restored.EgressOK || net.ParseIP(restored.ExitIP) == nil {
		applySlotSnapshot(&group, restored)
		group.Status = domain.ProxyGroupRepairRequired
		group.LastErrorCode = "rollback_failed"
		group.UpdatedAt = o.config.Now().UTC()
		_ = o.save(ctx, &group)
		return domain.ProxyGroup{}, &Error{Code: "repair_required"}
	}
	applySlotSnapshot(&group, restored)
	group.Status = domain.ProxyGroupReady
	group.LastErrorCode = ""
	group.LastCheckedAt = o.config.Now().UTC()
	group.LastSeenAt = group.LastCheckedAt
	group.UpdatedAt = group.LastCheckedAt
	if saveErr := o.save(ctx, &group); saveErr != nil {
		return domain.ProxyGroup{}, saveErr
	}
	return domain.ProxyGroup{}, operationError(cause)
}

func (o *Orchestrator) CheckMain(ctx context.Context) (store.MainEgress, error) {
	status, err := o.aimili.MainStatus(ctx)
	if err != nil {
		return store.MainEgress{}, operationError(err)
	}
	if !status.Active || !status.EgressOK || status.Port != 7928 || net.ParseIP(status.ExitIP) == nil {
		return store.MainEgress{}, &Error{Code: "not_ready"}
	}
	manager, ok := o.xui.(legacyMainXUIClient)
	if !ok {
		return store.MainEgress{}, &Error{Code: "not_configured"}
	}
	policy, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return store.MainEgress{}, err
	}
	legacy, err := manager.EnsureLegacyMain(ctx, xui.LegacyMainDesired{VLESSPort: 8443, MixedPort: o.config.MainMixedPort, SOCKSPort: 7928, MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword), MixedSourceRestrictionEnabled: policy.Enabled, MixedSourceCIDRs: prefixStrings(policy.CIDRs), RealityTarget: "127.0.0.1:443", RealityServerName: o.config.PublicHost})
	if err != nil {
		return store.MainEgress{}, operationError(err)
	}
	mainGroup := domain.ProxyGroup{PublicPort: 8443, MixedPort: o.config.MainMixedPort, ExitIP: status.ExitIP, RealityPublicKey: legacy.PublicKey, RealityShortID: legacy.ShortID, RealityServerName: legacy.ServerName, RealityMLDSA65Verify: legacy.MLDSA65Verify}
	socksResult, vlessResult, err := o.waitForMainValidation(ctx, mainGroup, credentials)
	if err != nil {
		return store.MainEgress{}, operationError(err)
	}
	proxyType := domain.ProxyType(status.ProxyType)
	if !proxyType.Valid() {
		proxyType = domain.ProxyTypeDatacenter
	}
	country := strings.ToUpper(status.Country)
	if len(country) != 2 {
		country = "ZZ"
	}
	now := o.config.Now().UTC()
	result := store.MainEgress{ResourceName: "agw-main", CountryCode: country, CountryName: status.CountryName, ProxyType: proxyType, CandidateID: status.CandidateID, ExitIP: status.ExitIP, PublicInboundID: legacy.VLESSInboundID, MixedInboundID: legacy.MixedInboundID, PublicPort: legacy.VLESSPort, MixedPort: legacy.MixedPort, Enabled: true, VLESSLatencyMS: durationMillis(vlessResult.Latency), SOCKSLatencyMS: durationMillis(socksResult.Latency), LastCheckedAt: now, UpdatedAt: now}
	if err := o.store.SaveMainEgress(ctx, result); err != nil {
		return store.MainEgress{}, &Error{Code: "storage_failed"}
	}
	return result, nil
}

func (o *Orchestrator) waitForMainValidation(ctx context.Context, group domain.ProxyGroup, credentials runtimeCredentials) (validator.Result, validator.Result, error) {
	waitCtx, cancel := context.WithTimeout(ctx, o.config.ReadyTimeout)
	defer cancel()
	for {
		socksResult, socksErr := o.validateSOCKS(waitCtx, group, credentials)
		vlessResult, vlessErr := o.validateVLESS(waitCtx, group, credentials)
		if socksErr == nil && vlessErr == nil {
			return socksResult, vlessResult, nil
		}
		lastErr := socksErr
		if lastErr == nil {
			lastErr = vlessErr
		}
		if !retryableMainValidation(socksErr) || !retryableMainValidation(vlessErr) {
			return validator.Result{}, validator.Result{}, lastErr
		}
		timer := time.NewTimer(o.config.PollInterval)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return validator.Result{}, validator.Result{}, lastErr
		case <-timer.C:
		}
	}
}

func retryableMainValidation(err error) bool {
	if err == nil {
		return true
	}
	switch errorCode(err) {
	case "connection_failed", "dns_failed", "protocol_failed", "timeout":
		return true
	default:
		return false
	}
}
