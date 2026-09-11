package orchestrator

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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

type subscriptionReadOnlyKey struct{}
type subscriptionAliasRepairKey struct{}

func (o *Orchestrator) verifySubscription(ctx context.Context) (SubscriptionResult, error) {
	return o.Subscription(context.WithValue(ctx, subscriptionReadOnlyKey{}, true))
}

func (o *Orchestrator) repairSubscriptionAliases(ctx context.Context) (SubscriptionResult, error) {
	return o.Subscription(context.WithValue(ctx, subscriptionAliasRepairKey{}, true))
}

func subscriptionAliases(main store.MainEgress, groups []domain.ProxyGroup) (map[int64]string, error) {
	mainCountry := strings.TrimSpace(main.CountryName)
	if main.ResourceName != "agw-main" || !main.Enabled || main.PublicInboundID < 1 || mainCountry == "" || mainCountry != main.CountryName || len(groups) == 0 {
		return nil, &Error{Code: "invalid_request"}
	}
	aliases := map[int64]string{main.PublicInboundID: "主连接_" + mainCountry}
	seenSlots := make(map[int]bool, len(groups))
	for _, group := range groups {
		country := strings.TrimSpace(group.CountryName)
		if !subscribableProxyGroup(group) || !strings.HasPrefix(group.ResourceName, "agw-") || group.ResourceName == "agw-main" ||
			group.AimiliSlot < 0 || group.PublicInboundID < 1 || country == "" || country != group.CountryName || seenSlots[group.AimiliSlot] {
			return nil, &Error{Code: "invalid_request"}
		}
		if _, duplicate := aliases[group.PublicInboundID]; duplicate {
			return nil, &Error{Code: "invalid_request"}
		}
		seenSlots[group.AimiliSlot] = true
		aliases[group.PublicInboundID] = fmt.Sprintf("出口位 %d_%s", group.AimiliSlot+1, country)
	}
	for slot := 0; slot < len(groups); slot++ {
		if !seenSlots[slot] {
			return nil, &Error{Code: "invalid_request"}
		}
	}
	return aliases, nil
}

func (o *Orchestrator) refreshDynamicSubscription(ctx context.Context) error {
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	mainStore, ok := o.store.(mainEgressStore)
	if !ok {
		return &Error{Code: "not_configured"}
	}
	main, err := mainStore.GetMainEgress(ctx)
	if err != nil {
		if errors.Is(err, store.ErrProxyGroupNotFound) {
			return nil
		}
		return err
	}
	if _, err := subscriptionAliases(main, groups); err != nil {
		// A partially provisioned or test-only slot set is not ready for an
		// alias refresh; keep the existing subscription and let reconciliation
		// complete the contiguous runtime set before rebuilding it.
		return nil
	}
	_, err = o.Subscription(ctx)
	return err
}

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
		if inbound.ID > 0 && inbound.Tag == "aimili-reality" && (inbound.Protocol == "vless" || inbound.Protocol == "hysteria") && inbound.Port == 8443 && inbound.Remark == "Aimili Reality" {
			ids = append(ids, inbound.ID)
		}
	}
	orderedGroups := append([]domain.ProxyGroup(nil), groups...)
	sort.SliceStable(orderedGroups, func(i, j int) bool {
		if orderedGroups[i].AimiliSlot == orderedGroups[j].AimiliSlot {
			return orderedGroups[i].ID < orderedGroups[j].ID
		}
		return orderedGroups[i].AimiliSlot < orderedGroups[j].AimiliSlot
	})
	for _, group := range orderedGroups {
		if subscribableProxyGroup(group) && group.PublicInboundID > 0 {
			ids = append(ids, group.PublicInboundID)
		}
	}
	if len(ids) == 0 {
		return SubscriptionResult{}, &Error{Code: "not_ready"}
	}
	var aliases map[int64]string
	if len(groups) > 0 && len(ids) == len(groups)+1 {
		mainStore, ok := o.store.(mainEgressStore)
		if ok {
			main, mainErr := mainStore.GetMainEgress(ctx)
			if mainErr == nil && main.Enabled {
				aliases, err = subscriptionAliases(main, groups)
				if err != nil || len(aliases) != len(ids) {
					return SubscriptionResult{}, &Error{Code: "not_ready"}
				}
				for _, id := range ids {
					if aliases[id] == "" {
						return SubscriptionResult{}, &Error{Code: "not_ready"}
					}
				}
			}
		}
	}
	credentials, err := o.runtimeCredentials(ctx)
	if err != nil {
		return SubscriptionResult{}, err
	}
	persisted, err := persistence.GetGatewaySubscription(ctx)
	if err != nil {
		return SubscriptionResult{}, &Error{Code: "storage_failed"}
	}
	persistedID := ""
	if persisted.ResourceName == "aimili-gateway-subscription" {
		persistedID = persisted.SubscriptionID
	}
	desired := xui.SubscriptionDesired{
		ClientEmail: "aimili-gateway-subscription", ClientUUID: string(credentials.vlessID), SubscriptionID: persistedID, InboundIDs: ids, Aliases: aliases,
	}
	var subscription xui.Subscription
	if readOnly, _ := ctx.Value(subscriptionReadOnlyKey{}).(bool); readOnly {
		subscription, err = manager.VerifySubscriptionClient(ctx, desired)
	} else if repairAliases, _ := ctx.Value(subscriptionAliasRepairKey{}).(bool); repairAliases {
		subscription, err = manager.RepairSubscriptionAliases(ctx, desired)
	} else {
		subscription, err = manager.EnsureSubscriptionClient(ctx, desired)
	}
	if err != nil {
		return SubscriptionResult{}, operationError(err)
	}
	if err := xui.ValidateSubscriptionCoverage(subscription.InboundIDs, ids); err != nil {
		return SubscriptionResult{}, operationError(err)
	}
	if aliases != nil {
		if err := xui.ValidateSubscriptionAliases(subscription.Aliases, aliases); err != nil {
			return SubscriptionResult{}, operationError(err)
		}
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
	if len(subscription.PublicProfiles) != len(ids) {
		return SubscriptionResult{}, &Error{Code: "subscription_incomplete"}
	}
	return SubscriptionResult{URL: strings.TrimRight(o.config.PublicOrigin, "/") + reference.EscapedPath(), InboundCount: len(ids), UpdatedAt: updatedAt, PublicProfiles: append([]xui.PublicProfile(nil), subscription.PublicProfiles...)}, nil
}

func subscribableProxyGroup(group domain.ProxyGroup) bool {
	switch group.Status {
	case domain.ProxyGroupReady, domain.ProxyGroupRotating, domain.ProxyGroupDegraded, domain.ProxyGroupRepairRequired:
		return true
	default:
		return false
	}
}

// CleanupLegacyAggregate removes the retired single-entry balancer only after
// the replacement subscription has been reconciled and its inbound coverage
// has been checked again. Database backups remain a deployment responsibility.
func (o *Orchestrator) CleanupLegacyAggregate(ctx context.Context) (LegacyAggregateCleanup, error) {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
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
	if strings.TrimSpace(candidateID) == "" {
		return domain.ProxyGroup{}, &Error{Code: "invalid_request"}
	}
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	if targetGroupID == "agw-main" {
		return o.replaceMainCandidate(ctx, candidateID)
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
	if (group.Status != domain.ProxyGroupReady && group.Status != domain.ProxyGroupDegraded && group.Status != domain.ProxyGroupRepairRequired) || group.AimiliSlot < 0 {
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
	if err != nil {
		var adapterError *aimili.AdapterError
		if errors.As(err, &adapterError) && adapterError.CandidateRejected {
			_, _ = o.aimili.Candidates(ctx)
		}
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
	group.ExitIPCheckedAt = assigned.CheckedAt
	_, credentials, inputErr := o.runtimeInputs(ctx)
	if inputErr == nil {
		var socksResult, vlessResult validator.Result
		socksResult, inputErr = o.validateSOCKS(ctx, group, credentials)
		if inputErr == nil {
			vlessResult, inputErr = o.validateCurrentPublic(ctx, group)
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
	if err := o.refreshDynamicSubscription(ctx); err != nil {
		return o.rollbackCandidateReplacement(ctx, group, previousSlot, assigner, err)
	}
	return group, nil
}

func (o *Orchestrator) replaceMainCandidate(ctx context.Context, candidateID string) (domain.ProxyGroup, error) {
	manager, ok := o.aimili.(mainAssignmentAimiliClient)
	if !ok {
		return domain.ProxyGroup{}, &Error{Code: "not_configured"}
	}
	assignment, err := manager.MainAssignment(ctx)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	if assignment.State == "pending_commit" || assignment.State == "repair_required" || assignment.State == "pending_gateway_validation" {
		return o.repairMainCandidate(ctx, manager, assignment, candidateID)
	}
	current, err := o.aimili.MainStatus(ctx)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	if current.Active && current.EgressOK && current.CandidateID != "" && mainCandidateMatches(candidateID, current.CandidateID, current.Country, current.ProxyType) {
		checked, checkErr := o.checkMain(ctx, false)
		if checkErr != nil {
			return domain.ProxyGroup{}, checkErr
		}
		if saveErr := o.store.SaveMainEgress(ctx, checked); saveErr != nil {
			return domain.ProxyGroup{}, &Error{Code: "storage_failed"}
		}
		return mainEgressGroup(checked), nil
	}
	if !current.Active || !current.EgressOK || current.CandidateID == "" {
		return domain.ProxyGroup{}, &Error{Code: "not_ready"}
	}
	candidates, err := o.aimili.Candidates(ctx)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	var candidate *aimili.Candidate
	for index := range candidates {
		item := &candidates[index]
		identity, identityErr := domain.NewProxyGroupIdentity(item.CountryCode, domain.ProxyType(item.ProxyType), item.ID)
		if identityErr == nil && (identity.ID == strings.TrimSpace(candidateID) || item.ID == strings.TrimSpace(candidateID)) && item.ProbeStatus == "available" {
			candidate = item
			break
		}
	}
	if candidate == nil {
		return domain.ProxyGroup{}, &Error{Code: "not_found"}
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{current.CandidateID, candidate.ID, candidate.CountryCode, candidate.ProxyType}, "\x00")))
	staged, err := manager.StageMainAssignment(ctx, aimili.MainAssignmentRequest{
		CandidateID:                candidate.ID,
		Country:                    candidate.CountryCode,
		ProxyType:                  candidate.ProxyType,
		ExpectedCurrentCandidateID: current.CandidateID,
		IdempotencyKey:             fmt.Sprintf("gateway-%x", digest[:]),
	})
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	if staged.State != "pending_commit" || !staged.DNSVerified || !staged.ExitVerified || !staged.Available {
		return o.rollbackMainCandidate(ctx, manager, staged.OperationID, &Error{Code: "egress_unavailable"})
	}
	checked, err := o.checkMain(ctx, false)
	if err != nil {
		return o.rollbackMainCandidate(ctx, manager, staged.OperationID, err)
	}
	if err := o.store.SaveMainEgress(ctx, checked); err != nil {
		return o.rollbackMainCandidate(ctx, manager, staged.OperationID, &Error{Code: "storage_failed"})
	}
	if err := o.refreshDynamicSubscription(ctx); err != nil {
		return o.rollbackMainCandidate(ctx, manager, staged.OperationID, err)
	}
	committed, err := commitMainAssignmentIdempotently(ctx, manager, staged.OperationID)
	if err != nil || committed.State != "committed" {
		if err == nil {
			err = &Error{Code: "commit_failed"}
		}
		return o.rollbackMainCandidate(ctx, manager, staged.OperationID, err)
	}
	return mainEgressGroup(checked), nil
}

func (o *Orchestrator) repairMainCandidate(ctx context.Context, manager mainAssignmentAimiliClient, assignment aimili.MainAssignmentStatus, candidateID string) (domain.ProxyGroup, error) {
	operationID := assignment.OperationID
	if operationID == "" {
		return domain.ProxyGroup{}, &Error{Code: "conflict"}
	}
	targetMatches := mainCandidateMatches(candidateID, assignment.NewCandidateID, assignment.Country, assignment.ProxyType)
	pending := assignment
	if assignment.State == "pending_commit" {
		if !targetMatches {
			return domain.ProxyGroup{}, &Error{Code: "conflict"}
		}
	} else if assignment.State == "pending_gateway_validation" {
		if !targetMatches {
			return domain.ProxyGroup{}, &Error{Code: "conflict"}
		}
	} else if targetMatches {
		var repairErr error
		for attempt := 0; attempt < 2; attempt++ {
			pending, repairErr = manager.RepairCommitMainAssignment(ctx, operationID)
			if repairErr == nil {
				break
			}
		}
		if repairErr != nil {
			return domain.ProxyGroup{}, operationError(repairErr)
		}
	} else {
		candidates, err := o.aimili.Candidates(ctx)
		if err != nil {
			return domain.ProxyGroup{}, operationError(err)
		}
		var candidate *aimili.Candidate
		for index := range candidates {
			item := &candidates[index]
			if item.ProbeStatus == "available" && domain.ProxyType(item.ProxyType).Valid() && mainCandidateMatches(candidateID, item.ID, item.CountryCode, item.ProxyType) {
				candidate = item
				break
			}
		}
		if candidate == nil {
			return domain.ProxyGroup{}, &Error{Code: "not_found"}
		}
		request := aimili.MainRepairRequest{CandidateID: candidate.ID, Country: candidate.CountryCode, ProxyType: candidate.ProxyType}
		var repairErr error
		for attempt := 0; attempt < 2; attempt++ {
			pending, repairErr = manager.RepairReplaceMainAssignment(ctx, operationID, request)
			if repairErr == nil {
				break
			}
		}
		if repairErr != nil {
			return domain.ProxyGroup{}, operationError(repairErr)
		}
	}
	if pending.State != "pending_commit" && pending.State != "pending_gateway_validation" && pending.State != "committed" {
		return domain.ProxyGroup{}, &Error{Code: "egress_unavailable"}
	}
	if pending.State != "committed" && (!pending.DNSVerified || !pending.ExitVerified || !pending.Available) {
		return domain.ProxyGroup{}, &Error{Code: "egress_unavailable"}
	}
	checked, err := o.checkMain(ctx, false)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	if strings.TrimSpace(checked.CandidateID) != strings.TrimSpace(pending.NewCandidateID) ||
		checked.CountryCode != normalizedMainCountry(pending.Country) ||
		checked.ProxyType != normalizedMainProxyType(pending.ProxyType) {
		return domain.ProxyGroup{}, &Error{Code: "conflict"}
	}
	if pending.State != "committed" {
		committed, commitErr := commitMainAssignmentIdempotently(ctx, manager, operationID)
		if commitErr != nil || committed.State != "committed" {
			if commitErr == nil {
				commitErr = &Error{Code: "commit_failed"}
			}
			return domain.ProxyGroup{}, operationError(commitErr)
		}
	}
	if err := o.store.SaveMainEgress(ctx, checked); err != nil {
		return domain.ProxyGroup{}, &Error{Code: "storage_failed"}
	}
	if err := o.refreshDynamicSubscription(ctx); err != nil {
		return o.rollbackMainCandidate(ctx, manager, operationID, err)
	}
	return mainEgressGroup(checked), nil
}

func commitMainAssignmentIdempotently(ctx context.Context, manager mainAssignmentAimiliClient, operationID string) (aimili.MainAssignmentStatus, error) {
	var result aimili.MainAssignmentStatus
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		result, err = manager.CommitMainAssignment(ctx, operationID)
		if err == nil {
			return result, nil
		}
	}
	return aimili.MainAssignmentStatus{}, err
}

func mainCandidateMatches(requested, rawID, country, proxyType string) bool {
	requested = strings.TrimSpace(requested)
	if requested == strings.TrimSpace(rawID) {
		return true
	}
	identity, err := domain.NewProxyGroupIdentity(country, domain.ProxyType(strings.ToLower(strings.TrimSpace(proxyType))), rawID)
	return err == nil && requested == identity.ID
}

func (o *Orchestrator) rollbackMainCandidate(ctx context.Context, manager mainAssignmentAimiliClient, operationID string, cause error) (domain.ProxyGroup, error) {
	rolled, rollbackErr := manager.RollbackMainAssignment(ctx, operationID)
	if rollbackErr != nil || rolled.State != "rolled_back" {
		return domain.ProxyGroup{}, &Error{Code: "repair_required"}
	}
	if _, verifyErr := o.checkMain(ctx, true); verifyErr != nil {
		return domain.ProxyGroup{}, &Error{Code: "repair_required"}
	}
	if subscriptionErr := o.refreshDynamicSubscription(ctx); subscriptionErr != nil {
		return domain.ProxyGroup{}, &Error{Code: "repair_required"}
	}
	return domain.ProxyGroup{}, operationError(cause)
}

func mainEgressGroup(value store.MainEgress) domain.ProxyGroup {
	return domain.ProxyGroup{
		ID: value.ResourceName, ResourceName: value.ResourceName,
		CountryCode: value.CountryCode, CountryName: value.CountryName,
		ProxyType: value.ProxyType, CandidateID: value.CandidateID,
		Status: domain.ProxyGroupReady, EgressSource: domain.EgressSourceMain,
		AimiliSlot: -1, PublicPort: value.PublicPort, MixedPort: value.MixedPort,
		ExitIP: value.ExitIP, PublicInboundID: value.PublicInboundID,
		MixedInboundID: value.MixedInboundID, VLESSLatencyMS: value.VLESSLatencyMS,
		SOCKSLatencyMS: value.SOCKSLatencyMS, LastCheckedAt: value.LastCheckedAt,
		UpdatedAt: value.UpdatedAt, Version: 1,
	}
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
	if subscriptionErr := o.refreshDynamicSubscription(ctx); subscriptionErr != nil {
		group.Status = domain.ProxyGroupRepairRequired
		group.LastErrorCode = "rollback_failed"
		group.UpdatedAt = o.config.Now().UTC()
		_ = o.save(ctx, &group)
		return domain.ProxyGroup{}, &Error{Code: "repair_required"}
	}
	return domain.ProxyGroup{}, operationError(cause)
}

func (o *Orchestrator) CheckMain(ctx context.Context) (store.MainEgress, error) {
	checked, err := o.checkMain(ctx, true)
	if err != nil {
		return store.MainEgress{}, err
	}
	// Current-public validation reads the subscription before the newly observed
	// main identity is persisted. Refresh the four dynamic display aliases after
	// persistence so a background main drift cannot leave a stale country label.
	if err := o.refreshDynamicSubscription(ctx); err != nil {
		return store.MainEgress{}, err
	}
	return checked, nil
}

func (o *Orchestrator) checkMain(ctx context.Context, persist bool) (store.MainEgress, error) {
	status, err := o.aimili.MainStatus(ctx)
	if err != nil {
		return store.MainEgress{}, operationError(err)
	}
	if !status.Active || !status.EgressOK || status.Port != 7928 || net.ParseIP(status.ExitIP) == nil {
		return store.MainEgress{}, &Error{Code: "not_ready"}
	}
	if protocols, ok := o.store.(protocolModeStore); ok {
		if protocol, protocolErr := protocols.GetEgressProtocolMode(ctx, "agw-main"); protocolErr == nil && protocol.State == domain.ProtocolReady && protocol.ActiveMode.Valid() {
			mainStore, mainOK := o.store.(mainEgressStore)
			if !mainOK {
				return store.MainEgress{}, &Error{Code: "not_configured"}
			}
			stored, storedErr := mainStore.GetMainEgress(ctx)
			if storedErr != nil || !stored.Enabled || stored.PublicInboundID < 1 || stored.PublicPort != 8443 || stored.MixedPort < 1 {
				return store.MainEgress{}, &Error{Code: "not_ready"}
			}
			_, credentials, credentialsErr := o.runtimeInputs(ctx)
			if credentialsErr != nil {
				return store.MainEgress{}, credentialsErr
			}
			group := mainEgressGroup(stored)
			group.ExitIP = status.ExitIP
			socksResult, publicResult, validationErr := o.waitForCurrentMainValidation(ctx, group, credentials)
			if validationErr != nil {
				return store.MainEgress{}, operationError(validationErr)
			}
			now := o.config.Now().UTC()
			stored.CandidateID = status.CandidateID
			stored.CountryCode = normalizedMainCountry(status.Country)
			stored.CountryName = status.CountryName
			stored.ProxyType = normalizedMainProxyType(status.ProxyType)
			stored.ExitIP = status.ExitIP
			stored.SOCKSLatencyMS = durationMillis(socksResult.Latency)
			stored.VLESSLatencyMS = durationMillis(publicResult.Latency)
			stored.LastCheckedAt = now
			stored.LastErrorCode = ""
			stored.UpdatedAt = now
			if persist {
				if err := o.store.SaveMainEgress(ctx, stored); err != nil {
					return store.MainEgress{}, &Error{Code: "storage_failed"}
				}
			}
			return stored, nil
		}
	}
	manager, ok := o.xui.(legacyMainXUIClient)
	if !ok {
		return store.MainEgress{}, &Error{Code: "not_configured"}
	}
	policy, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return store.MainEgress{}, err
	}
	legacy, err := manager.EnsureLegacyMain(ctx, xui.LegacyMainDesired{VLESSPort: 8443, MixedPort: o.config.MainMixedPort, SOCKSPort: 7928, VLESSClientID: string(credentials.vlessID), MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword), MixedSourceRestrictionEnabled: policy.Enabled, MixedSourceCIDRs: prefixStrings(policy.CIDRs), RealityTarget: "127.0.0.1:443", RealityServerName: o.config.RealityServerName})
	if err != nil {
		return store.MainEgress{}, operationError(err)
	}
	mainGroup := domain.ProxyGroup{PublicPort: 8443, MixedPort: o.config.MainMixedPort, ExitIP: status.ExitIP, RealityPublicKey: legacy.PublicKey, RealityShortID: legacy.ShortID, RealityServerName: legacy.ServerName, RealityMLDSA65Verify: legacy.MLDSA65Verify}
	socksResult, vlessResult, err := o.waitForMainValidation(ctx, mainGroup, credentials)
	if err != nil {
		return store.MainEgress{}, operationError(err)
	}
	proxyType := normalizedMainProxyType(status.ProxyType)
	country := normalizedMainCountry(status.Country)
	now := o.config.Now().UTC()
	result := store.MainEgress{ResourceName: "agw-main", CountryCode: country, CountryName: status.CountryName, ProxyType: proxyType, CandidateID: status.CandidateID, ExitIP: status.ExitIP, PublicInboundID: legacy.VLESSInboundID, MixedInboundID: legacy.MixedInboundID, PublicPort: legacy.VLESSPort, MixedPort: legacy.MixedPort, Enabled: true, VLESSLatencyMS: durationMillis(vlessResult.Latency), SOCKSLatencyMS: durationMillis(socksResult.Latency), LastCheckedAt: now, UpdatedAt: now}
	if persist {
		if err := o.store.SaveMainEgress(ctx, result); err != nil {
			return store.MainEgress{}, &Error{Code: "storage_failed"}
		}
		protocols, ok := o.store.(protocolModeStore)
		if !ok {
			return store.MainEgress{}, &Error{Code: "not_configured"}
		}
		if _, protocolErr := protocols.GetEgressProtocolMode(ctx, "agw-main"); errors.Is(protocolErr, store.ErrEgressProtocolNotFound) {
			if err := protocols.CreateEgressProtocolMode(ctx, domain.EgressProtocolMode{
				EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision,
				State: domain.ProtocolReady, Version: 1, UpdatedAt: now,
			}); err != nil {
				return store.MainEgress{}, &Error{Code: "storage_failed"}
			}
		} else if protocolErr != nil {
			return store.MainEgress{}, &Error{Code: "storage_failed"}
		}
	}
	return result, nil
}

func normalizedMainProxyType(value string) domain.ProxyType {
	proxyType := domain.ProxyType(strings.ToLower(strings.TrimSpace(value)))
	if !proxyType.Valid() {
		return domain.ProxyTypeDatacenter
	}
	return proxyType
}

func normalizedMainCountry(value string) string {
	country := strings.ToUpper(strings.TrimSpace(value))
	if len(country) != 2 {
		return "ZZ"
	}
	return country
}

func (o *Orchestrator) waitForCurrentMainValidation(ctx context.Context, group domain.ProxyGroup, credentials runtimeCredentials) (validator.Result, validator.Result, error) {
	waitCtx, cancel := context.WithTimeout(ctx, o.config.ReadyTimeout)
	defer cancel()
	for {
		socksResult, socksErr := o.validateSOCKS(waitCtx, group, credentials)
		publicResult, publicErr := o.validateCurrentPublic(waitCtx, group)
		if socksErr == nil && publicErr == nil {
			return socksResult, publicResult, nil
		}
		lastErr := socksErr
		if lastErr == nil {
			lastErr = publicErr
		}
		if !retryableMainValidation(socksErr) || !retryableMainValidation(publicErr) {
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
