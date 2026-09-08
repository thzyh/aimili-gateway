package orchestrator

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type ReconcileResult struct {
	Discovered int `json:"discovered"`
	Ready      int `json:"ready"`
	Failed     int `json:"failed"`
}

type candidateReassignmentStore interface {
	ReassignProxyGroupCandidates(context.Context, map[string]string, time.Time) error
}

// Reconcile converges every safe Aimili candidate into an independently usable proxy entry.
// A single candidate failure is isolated so healthy candidates can still become ready.
func (o *Orchestrator) Reconcile(ctx context.Context) ReconcileResult {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	unlock := o.locks.lock("activation")
	defer unlock()
	result := ReconcileResult{}
	candidates, err := o.aimili.Candidates(ctx)
	if err != nil {
		result.Failed = 1
		return result
	}
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		result.Failed = 1
		return result
	}
	groups = o.adoptLegacyGroups(ctx, groups, candidates)
	groups, historyFailures := o.degradeHistoricalDuplicateExits(ctx, groups)
	result.Failed += historyFailures
	groups, adoptionFailures := o.adoptUnmanagedSlots(ctx, groups)
	result.Failed += adoptionFailures
	groups = o.refreshAssignedGroups(ctx, groups)
	existing := make(map[string]domain.ProxyGroup, len(groups))
	byExit := make(map[string]domain.ProxyGroup, len(groups))
	activeCount := len(groups)
	for _, group := range groups {
		if group.CandidateID != "" {
			existing[group.CandidateID] = group
		}
		if group.Status == domain.ProxyGroupReady {
			if normalized, ok := normalizeExitIP(group.ExitIP); ok {
				byExit[normalized] = group
			}
		}
	}
	for _, candidate := range candidates {
		candidate.ID = strings.TrimSpace(candidate.ID)
		proxyType := domain.ProxyType(candidate.ProxyType)
		if candidate.ID == "" || candidate.ProbeStatus != "available" || !proxyType.Valid() {
			continue
		}
		result.Discovered++
		if group, ok := existing[candidate.ID]; ok {
			if group.Status != domain.ProxyGroupReady {
				result.Failed++
			}
			continue
		}
		if activeCount >= o.config.MaxGroups {
			continue
		}
		group, enableErr := o.Enable(ctx, EnableRequest{
			CountryCode: candidate.CountryCode, ProxyType: proxyType,
			CandidateID: candidate.ID, CandidateIP: candidate.IP, CandidateLatencyMS: candidate.LatencyMS,
		})
		if enableErr != nil || group.Status != domain.ProxyGroupReady {
			result.Failed++
			continue
		}
		existing[candidate.ID] = group
		activeCount++
		normalizedExit, _ := normalizeExitIP(group.ExitIP)
		if current, duplicate := byExit[normalizedExit]; duplicate && current.ID != group.ID {
			keep, remove := current, group
			if egressScore(group) < egressScore(current) {
				keep, remove = group, current
			}
			if disableErr := o.Disable(ctx, remove.ID); disableErr != nil {
				result.Failed++
			} else {
				activeCount--
				delete(existing, remove.CandidateID)
				byExit[normalizedExit] = keep
			}
		} else {
			byExit[normalizedExit] = group
		}
	}
	if finalGroups, listErr := o.store.ListProxyGroups(ctx); listErr == nil {
		for _, group := range finalGroups {
			if group.Status == domain.ProxyGroupReady {
				result.Ready++
			}
		}
	}
	// Subscription convergence is intentionally best-effort here. Reconcile's
	// group result remains useful when 3x-ui subscription support is temporarily
	// unavailable; the authenticated subscription endpoint surfaces that error.
	_, _ = o.Subscription(ctx)
	return result
}

// refreshAssignedGroups revalidates degraded fixed groups against their current
// AimiliVPN slot. A slot can legitimately switch to a different candidate while
// retaining its number; without this pass Reconcile would keep the stale
// candidate ID forever and report slot_not_found on every subsequent run.
func (o *Orchestrator) refreshAssignedGroups(ctx context.Context, groups []domain.ProxyGroup) []domain.ProxyGroup {
	slots, slotsErr := o.aimili.ListSlots(ctx)
	bySlot := make(map[int]aimili.Slot, len(slots))
	if slotsErr == nil {
		for _, slot := range slots {
			bySlot[slot.Number] = slot
		}
	}
	drifted := make(map[string]struct{})
	if slotsErr == nil && len(groups) > 0 {
		assignments := make(map[string]string, len(groups))
		seenCandidates := make(map[string]struct{}, len(groups))
		complete := true
		for _, group := range groups {
			slot, ok := bySlot[group.AimiliSlot]
			candidateID := strings.TrimSpace(slot.NodeID)
			if !ok || !slot.EgressOK || (slot.Status != "up" && slot.Status != "ready") || candidateID == "" {
				complete = false
				break
			}
			if _, duplicate := seenCandidates[candidateID]; duplicate {
				complete = false
				break
			}
			seenCandidates[candidateID] = struct{}{}
			assignments[group.ID] = candidateID
			if candidateID != strings.TrimSpace(group.CandidateID) {
				drifted[group.ID] = struct{}{}
			}
		}
		if complete && len(drifted) > 0 {
			persistence, ok := o.store.(candidateReassignmentStore)
			if !ok {
				drifted = map[string]struct{}{}
			} else if err := persistence.ReassignProxyGroupCandidates(ctx, assignments, o.config.Now().UTC()); err != nil {
				log.Printf("reconcile candidate reassignment failed: code=%s", errorCode(err))
				drifted = map[string]struct{}{}
			} else if reloaded, err := o.store.ListProxyGroups(ctx); err == nil {
				groups = reloaded
			} else {
				log.Printf("reconcile candidate reload failed: code=%s", errorCode(err))
				drifted = map[string]struct{}{}
			}
		}
	}
	for index := range groups {
		group := groups[index]
		missingManagedResources := group.PublicInboundID <= 0 || group.MixedInboundID <= 0
		_, assignmentDrift := drifted[group.ID]
		refreshable := assignmentDrift || group.LastErrorCode == "slot_not_found" || (group.LastErrorCode == "protocol_failed" && missingManagedResources)
		if (group.Status == domain.ProxyGroupReady && !assignmentDrift) || !refreshable || group.AimiliSlot < 0 || !strings.HasPrefix(group.ID, "agw-") {
			continue
		}
		if missingManagedResources && slotsErr == nil {
			if slot, ok := bySlot[group.AimiliSlot]; ok && slot.EgressOK {
				policy, credentials, inputsErr := o.runtimeInputs(ctx)
				if inputsErr == nil {
					applySlotSnapshot(&group, slot)
					refreshed, provisionErr := o.provisionGroupForSlot(ctx, group, slot, policy, credentials, false)
					if provisionErr == nil {
						groups[index] = refreshed
						continue
					}
					log.Printf("reconcile assigned group reprovision failed: id=%s slot=%d code=%s", group.ID, group.AimiliSlot, errorCode(provisionErr))
				}
			}
		}
		refreshed, err := o.Check(ctx, group.ID)
		if refreshed.ID != "" {
			groups[index] = refreshed
		}
		if err != nil {
			log.Printf("reconcile assigned group refresh failed: id=%s slot=%d code=%s", group.ID, group.AimiliSlot, errorCode(err))
		}
	}
	return groups
}

func (o *Orchestrator) adoptUnmanagedSlots(ctx context.Context, groups []domain.ProxyGroup) ([]domain.ProxyGroup, int) {
	slots, err := o.aimili.ListSlots(ctx)
	if err != nil {
		return groups, 1
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Number < slots[j].Number })
	claimedSlots := make(map[int]struct{}, len(groups))
	claimedCandidates := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		claimedSlots[group.AimiliSlot] = struct{}{}
		if candidateID := strings.TrimSpace(group.CandidateID); candidateID != "" {
			claimedCandidates[candidateID] = struct{}{}
		}
	}
	failures := 0
	for _, slot := range slots {
		candidateID := strings.TrimSpace(slot.NodeID)
		if len(groups) >= o.config.MaxGroups || candidateID == "" || !slot.EgressOK || (slot.Status != "up" && slot.Status != "ready") {
			continue
		}
		if _, claimed := claimedSlots[slot.Number]; claimed {
			continue
		}
		if _, claimed := claimedCandidates[candidateID]; claimed {
			continue
		}
		group, adoptErr := o.adoptExistingSlot(ctx, slot, groups)
		if adoptErr != nil {
			log.Printf("reconcile slot adoption failed: slot=%d code=%s", slot.Number, errorCode(adoptErr))
			failures++
			continue
		}
		groups = append(groups, group)
		claimedSlots[group.AimiliSlot] = struct{}{}
		claimedCandidates[group.CandidateID] = struct{}{}
	}
	return groups, failures
}

func (o *Orchestrator) degradeHistoricalDuplicateExits(ctx context.Context, groups []domain.ProxyGroup) ([]domain.ProxyGroup, int) {
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].CreatedAt.Equal(groups[j].CreatedAt) {
			return groups[i].ID < groups[j].ID
		}
		return groups[i].CreatedAt.Before(groups[j].CreatedAt)
	})
	seen := make(map[string]string, len(groups))
	failures := 0
	for index := range groups {
		group := &groups[index]
		if group.Status != domain.ProxyGroupReady {
			continue
		}
		normalized, ok := normalizeExitIP(group.ExitIP)
		if !ok {
			continue
		}
		if _, duplicate := seen[normalized]; !duplicate {
			seen[normalized] = group.ID
			continue
		}
		group.Status = domain.ProxyGroupDegraded
		group.LastErrorCode = "duplicate_exit_ip"
		group.UpdatedAt = o.config.Now().UTC()
		if err := o.save(ctx, group); err != nil {
			failures++
		}
	}
	return groups, failures
}

// Pool merges the safe Aimili candidate catalog with the bounded set of live
// groups. Entries beyond live capacity are visible but contain no live-only
// ports, verified exit IP, or connection material.
func (o *Orchestrator) Pool(ctx context.Context) ([]domain.ProxyGroup, error) {
	candidates, err := o.aimili.Candidates(ctx)
	if err != nil {
		return nil, operationError(err)
	}
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return nil, &Error{Code: "storage_failed"}
	}
	byCandidate := make(map[string]domain.ProxyGroup, len(groups))
	legacy := make([]domain.ProxyGroup, 0)
	for _, group := range groups {
		if strings.TrimSpace(group.CandidateID) == "" {
			legacy = append(legacy, group)
			continue
		}
		byCandidate[group.CandidateID] = group
	}
	result := make([]domain.ProxyGroup, 0, len(candidates)+len(legacy))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate.ID = strings.TrimSpace(candidate.ID)
		proxyType := domain.ProxyType(candidate.ProxyType)
		if candidate.ID == "" || candidate.ProbeStatus != "available" || !proxyType.Valid() {
			continue
		}
		seen[candidate.ID] = struct{}{}
		if group, ok := byCandidate[candidate.ID]; ok {
			result = append(result, group)
			continue
		}
		standby, identityErr := domain.NewProxyGroupIdentity(candidate.CountryCode, proxyType, candidate.ID)
		if identityErr != nil {
			continue
		}
		standby.CountryName = candidate.CountryName
		standby.CandidateIP = candidate.IP
		if normalizedExit, ok := normalizeExitIP(candidate.ExitIP); ok {
			standby.ExitIP = normalizedExit
			standby.ExitIPCheckedAt = candidate.ExitIPCheckedAt
		}
		standby.CandidateLatencyMS = candidate.LatencyMS
		standby.Status = domain.ProxyGroupStandby
		result = append(result, standby)
	}
	for candidateID, group := range byCandidate {
		if _, ok := seen[candidateID]; !ok {
			result = append(result, group)
		}
	}
	result = append(legacy, result...)
	if main, mainErr := o.aimili.MainStatus(ctx); mainErr == nil && main.Active {
		proxyType := domain.ProxyType(main.ProxyType)
		if !proxyType.Valid() {
			proxyType = domain.ProxyTypeDatacenter
		}
		country := strings.ToUpper(strings.TrimSpace(main.Country))
		if len(country) != 2 {
			country = "ZZ"
		}
		status := domain.ProxyGroupDegraded
		lastError := "egress_unavailable"
		if main.EgressOK {
			status, lastError = domain.ProxyGroupReady, ""
		}
		mainGroup := domain.ProxyGroup{ID: "agw-main", ResourceName: "agw-main", CountryCode: country, CountryName: main.CountryName, ProxyType: proxyType, CandidateID: main.CandidateID, Status: status, EgressSource: domain.EgressSourceMain, AimiliSlot: -1, PublicPort: 8443, MixedPort: o.config.MainMixedPort, ExitIP: main.ExitIP, LastErrorCode: lastError, Version: 1, LastCheckedAt: o.config.Now().UTC()}
		if source, ok := o.store.(mainEgressStore); ok {
			if stored, storedErr := source.GetMainEgress(ctx); storedErr == nil {
				mainGroup.CandidateLatencyMS = stored.CandidateLatencyMS
				mainGroup.VLESSLatencyMS = stored.VLESSLatencyMS
				mainGroup.SOCKSLatencyMS = stored.SOCKSLatencyMS
				mainGroup.LastCheckedAt = stored.LastCheckedAt
				if stored.LastErrorCode != "" {
					mainGroup.LastErrorCode = stored.LastErrorCode
				}
			}
		}
		for _, group := range result {
			if group.Status == domain.ProxyGroupReady && group.ExitIP != "" && group.ExitIP == mainGroup.ExitIP {
				mainGroup.Status = domain.ProxyGroupDegraded
				mainGroup.LastErrorCode = "duplicate_exit_ip"
			}
		}
		result = append([]domain.ProxyGroup{mainGroup}, result...)
	}
	if err := o.attachProtocolModes(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (o *Orchestrator) attachProtocolModes(ctx context.Context, groups []domain.ProxyGroup) error {
	persistence, ok := o.store.(protocolModeStore)
	if !ok {
		return &Error{Code: "not_configured"}
	}
	for index := range groups {
		group := &groups[index]
		if group.Status == domain.ProxyGroupStandby || !strings.HasPrefix(group.ID, "agw-") {
			continue
		}
		state, err := persistence.GetEgressProtocolMode(ctx, group.ID)
		if err != nil {
			if !errors.Is(err, store.ErrEgressProtocolNotFound) {
				return &Error{Code: "storage_failed"}
			}
			group.Status = domain.ProxyGroupRepairRequired
			group.ProtocolState = domain.ProtocolRepairRequired
			group.ProtocolLastErrorCode = "protocol_state_missing"
			continue
		}
		if !state.ActiveMode.Valid() || !state.DesiredMode.Valid() || !state.State.Valid() {
			group.Status = domain.ProxyGroupRepairRequired
			group.ProtocolState = domain.ProtocolRepairRequired
			group.ProtocolLastErrorCode = "protocol_state_invalid"
			continue
		}
		group.ProtocolMode = state.ActiveMode
		group.DesiredProtocolMode = state.DesiredMode
		group.ProtocolState = state.State
		group.ProtocolLastErrorCode = state.LastErrorCode
		if state.State == domain.ProtocolRepairRequired {
			group.Status = domain.ProxyGroupRepairRequired
		}
	}
	return nil
}

func (o *Orchestrator) adoptLegacyGroups(ctx context.Context, groups []domain.ProxyGroup, candidates []aimili.Candidate) []domain.ProxyGroup {
	hasLegacy := false
	claimed := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if strings.TrimSpace(group.CandidateID) == "" {
			hasLegacy = true
		} else {
			claimed[group.CandidateID] = struct{}{}
		}
	}
	if !hasLegacy {
		return groups
	}
	slots, err := o.aimili.ListSlots(ctx)
	if err != nil {
		return groups
	}
	bySlot := make(map[int]aimili.Slot, len(slots))
	for _, slot := range slots {
		bySlot[slot.Number] = slot
	}
	byCandidate := make(map[string]aimili.Candidate, len(candidates))
	for _, candidate := range candidates {
		candidate.ID = strings.TrimSpace(candidate.ID)
		if candidate.ID != "" && candidate.ProbeStatus == "available" {
			byCandidate[candidate.ID] = candidate
		}
	}
	for index := range groups {
		group := &groups[index]
		if strings.TrimSpace(group.CandidateID) != "" || group.Status != domain.ProxyGroupReady {
			continue
		}
		slot, ok := bySlot[group.AimiliSlot]
		if !ok || !slot.EgressOK || strings.TrimSpace(slot.NodeID) == "" {
			continue
		}
		candidate, ok := byCandidate[strings.TrimSpace(slot.NodeID)]
		if !ok || strings.ToUpper(candidate.CountryCode) != group.CountryCode || domain.ProxyType(candidate.ProxyType) != group.ProxyType {
			continue
		}
		if _, duplicate := claimed[candidate.ID]; duplicate {
			continue
		}
		group.CandidateID = candidate.ID
		group.CandidateIP = candidate.IP
		group.CandidateLatencyMS = candidate.LatencyMS
		if slot.CheckedAt > 0 {
			group.ExitIPCheckedAt = slot.CheckedAt
		}
		group.LastSeenAt = o.config.Now().UTC()
		group.UpdatedAt = group.LastSeenAt
		if err := o.save(ctx, group); err != nil {
			group.CandidateID = ""
			continue
		}
		claimed[candidate.ID] = struct{}{}
	}
	return groups
}

func egressScore(group domain.ProxyGroup) int {
	if group.VLESSLatencyMS > 0 || group.SOCKSLatencyMS > 0 {
		return group.VLESSLatencyMS + group.SOCKSLatencyMS
	}
	if group.CandidateLatencyMS > 0 {
		return group.CandidateLatencyMS * 2
	}
	return int(^uint(0) >> 1)
}
