package orchestrator

import (
	"context"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

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
		if group.Status == domain.ProxyGroupReady && group.VLESSInboundID > 0 {
			ids = append(ids, group.VLESSInboundID)
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
	previous := group
	assigned, err := assigner.AssignSlotNode(ctx, group.AimiliSlot, aimili.AssignSlotRequest{CandidateID: candidate.ID, Country: candidate.CountryCode, ProxyType: candidate.ProxyType})
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	if !assigned.EgressOK || net.ParseIP(assigned.ExitIP) == nil {
		_, _ = assigner.AssignSlotNode(ctx, previous.AimiliSlot, aimili.AssignSlotRequest{CandidateID: previous.CandidateID, Country: previous.CountryCode, ProxyType: string(previous.ProxyType)})
		return domain.ProxyGroup{}, &Error{Code: "egress_unavailable"}
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
		if _, rollbackErr := assigner.AssignSlotNode(ctx, previous.AimiliSlot, aimili.AssignSlotRequest{CandidateID: previous.CandidateID, Country: previous.CountryCode, ProxyType: string(previous.ProxyType)}); rollbackErr != nil {
			group.Status = domain.ProxyGroupRepairRequired
			group.LastErrorCode = "rollback_failed"
			group.UpdatedAt = o.config.Now().UTC()
			_ = o.save(ctx, &group)
			return domain.ProxyGroup{}, &Error{Code: "repair_required"}
		}
		return domain.ProxyGroup{}, operationError(inputErr)
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
	mainGroup := domain.ProxyGroup{VLESSPort: 8443, MixedPort: o.config.MainMixedPort, ExitIP: status.ExitIP, RealityPublicKey: legacy.PublicKey, RealityShortID: legacy.ShortID, RealityServerName: legacy.ServerName, RealityMLDSA65Verify: legacy.MLDSA65Verify}
	socksResult, err := o.validateSOCKS(ctx, mainGroup, credentials)
	if err != nil {
		return store.MainEgress{}, operationError(err)
	}
	vlessResult, err := o.validateVLESS(ctx, mainGroup, credentials)
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
	result := store.MainEgress{ResourceName: "agw-main", CountryCode: country, CountryName: status.CountryName, ProxyType: proxyType, ExitIP: status.ExitIP, VLESSInboundID: legacy.VLESSInboundID, MixedInboundID: legacy.MixedInboundID, VLESSPort: legacy.VLESSPort, MixedPort: legacy.MixedPort, Enabled: true, VLESSLatencyMS: durationMillis(vlessResult.Latency), SOCKSLatencyMS: durationMillis(socksResult.Latency), LastCheckedAt: now, UpdatedAt: now}
	if err := o.store.SaveMainEgress(ctx, result); err != nil {
		return store.MainEgress{}, &Error{Code: "storage_failed"}
	}
	return result, nil
}
