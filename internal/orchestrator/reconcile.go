package orchestrator

import (
	"context"
	"sort"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/domain"
)

type ReconcileResult struct {
	Discovered int `json:"discovered"`
	Ready      int `json:"ready"`
	Failed     int `json:"failed"`
}

// Reconcile converges every safe Aimili candidate into an independently usable proxy entry.
// A single candidate failure is isolated so healthy candidates can still become ready.
func (o *Orchestrator) Reconcile(ctx context.Context) ReconcileResult {
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
	return result
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
		standby.CandidateLatencyMS = candidate.LatencyMS
		standby.Status = domain.ProxyGroupStandby
		result = append(result, standby)
	}
	for candidateID, group := range byCandidate {
		if _, ok := seen[candidateID]; !ok {
			result = append(result, group)
		}
	}
	return append(legacy, result...), nil
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
