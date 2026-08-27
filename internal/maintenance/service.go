package maintenance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type Error struct{ Code string }

func (err *Error) Error() string { return "maintenance operation failed: " + err.Code }

type Config struct{ MaxOnline int }

type Summary struct {
	AccountSyncStatus store.AccountSyncStatus `json:"accountSyncStatus"`
	CandidateCount    int                     `json:"candidateCount"`
	OnlineCount       int                     `json:"onlineCount"`
	MaxOnline         int                     `json:"maxOnline"`
}

type AimiliSummary struct {
	CandidateCount   int       `json:"candidateCount"`
	ResidentialCount int       `json:"residentialCount"`
	DatacenterCount  int       `json:"datacenterCount"`
	ManagedSlotCount int       `json:"managedSlotCount"`
	LastRefreshedAt  time.Time `json:"lastRefreshedAt,omitempty"`
}

type XUISummary struct {
	ManagedVLESSCount    int       `json:"managedVlessCount"`
	ManagedMixedCount    int       `json:"managedMixedCount"`
	ManagedOutboundCount int       `json:"managedOutboundCount"`
	OwnershipMatches     bool      `json:"ownershipMatches"`
	LastCheckedAt        time.Time `json:"lastCheckedAt,omitempty"`
}

type aimiliSource interface {
	Candidates(context.Context) ([]aimili.Candidate, error)
	ListSlots(context.Context) ([]aimili.Slot, error)
	CheckSlot(context.Context, int) (aimili.SlotCheck, error)
}

type xuiSource interface {
	Snapshot(context.Context) (xui.Snapshot, error)
}

type groupSource interface {
	List(context.Context) ([]domain.ProxyGroup, error)
	RepairManaged(context.Context) error
}

type accountStatus interface {
	Status(context.Context) (store.AccountSyncState, error)
}

type Service struct {
	config   Config
	aimili   aimiliSource
	xui      xuiSource
	groups   groupSource
	accounts accountStatus
}

func New(config Config, aimiliClient aimiliSource, xuiClient xuiSource, groups groupSource, accounts accountStatus) (*Service, error) {
	if config.MaxOnline < 1 || aimiliClient == nil || xuiClient == nil || groups == nil || accounts == nil {
		return nil, errors.New("maintenance dependencies are required")
	}
	return &Service{config: config, aimili: aimiliClient, xui: xuiClient, groups: groups, accounts: accounts}, nil
}

func (service *Service) Summary(ctx context.Context) (Summary, error) {
	candidates, err := service.availableCandidates(ctx)
	if err != nil {
		return Summary{}, err
	}
	groups, err := service.groups.List(ctx)
	if err != nil {
		return Summary{}, &Error{Code: "service_unavailable"}
	}
	state, err := service.accounts.Status(ctx)
	if err != nil {
		return Summary{}, &Error{Code: "service_unavailable"}
	}
	return Summary{AccountSyncStatus: state.Status, CandidateCount: len(candidates), OnlineCount: len(groups), MaxOnline: service.config.MaxOnline}, nil
}

func (service *Service) AimiliVPN(ctx context.Context) (AimiliSummary, error) {
	candidates, err := service.availableCandidates(ctx)
	if err != nil {
		return AimiliSummary{}, err
	}
	slots, err := service.aimili.ListSlots(ctx)
	if err != nil {
		return AimiliSummary{}, &Error{Code: "service_unavailable"}
	}
	result := AimiliSummary{CandidateCount: len(candidates), ManagedSlotCount: len(slots)}
	for _, candidate := range candidates {
		switch domain.ProxyType(candidate.ProxyType) {
		case domain.ProxyTypeResidential:
			result.ResidentialCount++
		case domain.ProxyTypeDatacenter:
			result.DatacenterCount++
		}
		refreshed := time.Unix(int64(candidate.LastProbeAt), 0).UTC()
		if candidate.LastProbeAt > 0 && refreshed.After(result.LastRefreshedAt) {
			result.LastRefreshedAt = refreshed
		}
	}
	return result, nil
}

func (service *Service) RefreshAimiliVPN(ctx context.Context) (AimiliSummary, error) {
	return service.AimiliVPN(ctx)
}

func (service *Service) CheckAimiliVPN(ctx context.Context) (AimiliSummary, error) {
	groups, err := service.groups.List(ctx)
	if err != nil {
		return AimiliSummary{}, &Error{Code: "service_unavailable"}
	}
	for _, group := range groups {
		if _, err := service.aimili.CheckSlot(ctx, group.AimiliSlot); err != nil {
			return AimiliSummary{}, &Error{Code: "check_failed"}
		}
	}
	return service.AimiliVPN(ctx)
}

func (service *Service) XUI(ctx context.Context) (XUISummary, error) {
	snapshot, err := service.xui.Snapshot(ctx)
	if err != nil {
		return XUISummary{}, &Error{Code: "service_unavailable"}
	}
	groups, err := service.groups.List(ctx)
	if err != nil {
		return XUISummary{}, &Error{Code: "service_unavailable"}
	}
	result := XUISummary{OwnershipMatches: true}
	inbounds := make(map[int64]xui.Inbound, len(snapshot.Inbounds))
	for _, inbound := range snapshot.Inbounds {
		inbounds[inbound.ID] = inbound
	}
	outbounds := make(map[string]xui.Outbound, len(snapshot.Outbounds))
	for _, outbound := range snapshot.Outbounds {
		outbounds[outbound.Tag] = outbound
	}
	for _, group := range groups {
		vless, vlessOK := inbounds[group.VLESSInboundID]
		if vlessOK && vless.Tag == group.ResourceName+"-vless" && vless.Protocol == "vless" {
			result.ManagedVLESSCount++
		} else {
			result.OwnershipMatches = false
		}
		mixed, mixedOK := inbounds[group.MixedInboundID]
		if mixedOK && mixed.Tag == group.ResourceName+"-mixed" && mixed.Protocol == "mixed" {
			result.ManagedMixedCount++
		} else {
			result.OwnershipMatches = false
		}
		outbound, outboundOK := outbounds[group.ResourceName+"-socks"]
		if outboundOK && outbound.Protocol == "socks" {
			result.ManagedOutboundCount++
		} else {
			result.OwnershipMatches = false
		}
		if strings.TrimSpace(group.ConfigFingerprint) == "" {
			result.OwnershipMatches = false
		}
		if group.LastCheckedAt.After(result.LastCheckedAt) {
			result.LastCheckedAt = group.LastCheckedAt
		}
	}
	return result, nil
}

func (service *Service) CheckXUI(ctx context.Context) (XUISummary, error) { return service.XUI(ctx) }

func (service *Service) RepairXUI(ctx context.Context) (XUISummary, error) {
	if err := service.groups.RepairManaged(ctx); err != nil {
		return XUISummary{}, &Error{Code: "repair_failed"}
	}
	return service.XUI(ctx)
}

func (service *Service) availableCandidates(ctx context.Context) ([]aimili.Candidate, error) {
	candidates, err := service.aimili.Candidates(ctx)
	if err != nil {
		return nil, &Error{Code: "service_unavailable"}
	}
	result := make([]aimili.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ProbeStatus == "available" && domain.ProxyType(candidate.ProxyType).Valid() && len(strings.TrimSpace(candidate.CountryCode)) == 2 {
			result = append(result, candidate)
		}
	}
	return result, nil
}
