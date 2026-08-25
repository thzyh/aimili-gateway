package orchestrator

import (
	"context"
	"strings"

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
	unlock := o.locks.lock("reconcile")
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
	existing := make(map[string]domain.ProxyGroup, len(groups))
	byExit := make(map[string]domain.ProxyGroup, len(groups))
	for _, group := range groups {
		if group.CandidateID != "" {
			existing[group.CandidateID] = group
		}
		if group.Status == domain.ProxyGroupReady && group.ExitIP != "" {
			byExit[group.ExitIP] = group
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
		group, enableErr := o.Enable(ctx, EnableRequest{
			CountryCode: candidate.CountryCode, ProxyType: proxyType,
			CandidateID: candidate.ID, CandidateIP: candidate.IP, CandidateLatencyMS: candidate.LatencyMS,
		})
		if enableErr != nil || group.Status != domain.ProxyGroupReady {
			result.Failed++
			continue
		}
		existing[candidate.ID] = group
		if current, duplicate := byExit[group.ExitIP]; duplicate && current.ID != group.ID {
			keep, remove := current, group
			if egressScore(group) < egressScore(current) {
				keep, remove = group, current
			}
			if disableErr := o.Disable(ctx, remove.ID); disableErr != nil {
				result.Failed++
			} else {
				delete(existing, remove.CandidateID)
				byExit[group.ExitIP] = keep
			}
		} else {
			byExit[group.ExitIP] = group
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

func egressScore(group domain.ProxyGroup) int {
	if group.VLESSLatencyMS > 0 || group.SOCKSLatencyMS > 0 {
		return group.VLESSLatencyMS + group.SOCKSLatencyMS
	}
	if group.CandidateLatencyMS > 0 {
		return group.CandidateLatencyMS * 2
	}
	return int(^uint(0) >> 1)
}
