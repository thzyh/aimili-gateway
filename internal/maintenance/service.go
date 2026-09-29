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

type Config struct {
	MaxOnline       int
	LifetimeContext context.Context
	PollInterval    time.Duration
	PollTimeout     time.Duration
	Reconcile       func(context.Context)
}

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

type CapacitySummary = aimili.Capacity

type XUISummary struct {
	ManagedPublicCount   int       `json:"managedPublicCount"`
	ManagedVLESSCount    int       `json:"managedVlessCount"`
	ManagedMixedCount    int       `json:"managedMixedCount"`
	ManagedOutboundCount int       `json:"managedOutboundCount"`
	OwnershipMatches     bool      `json:"ownershipMatches"`
	LastCheckedAt        time.Time `json:"lastCheckedAt,omitempty"`
}

type aimiliSource interface {
	Candidates(context.Context) ([]aimili.Candidate, error)
	CandidateCountries(context.Context) ([]aimili.CandidateCountry, error)
	StartCountryRefresh(context.Context, string) (aimili.CountryRefresh, error)
	CountryRefresh(context.Context) (aimili.CountryRefresh, error)
	ListSlots(context.Context) ([]aimili.Slot, error)
	CheckSlot(context.Context, int) (aimili.SlotCheck, error)
}

type capacitySource interface {
	Capacity(context.Context) (aimili.Capacity, error)
	UpdateCapacity(context.Context, aimili.CapacityUpdate) (aimili.Capacity, error)
}

type dedicatedStandbySource interface {
	DedicatedStandbys(context.Context) ([]aimili.DedicatedStandby, error)
	ConfigureDedicatedStandbys(context.Context, []aimili.DedicatedStandbyConfig) ([]aimili.DedicatedStandby, error)
	AssignDedicatedStandby(context.Context, int, string) (aimili.DedicatedStandby, error)
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
	config       Config
	aimili       aimiliSource
	xui          xuiSource
	groups       groupSource
	accounts     accountStatus
	lifetime     context.Context
	pollInterval time.Duration
	pollTimeout  time.Duration
	reconcile    func(context.Context)
}

func New(config Config, aimiliClient aimiliSource, xuiClient xuiSource, groups groupSource, accounts accountStatus) (*Service, error) {
	if config.MaxOnline < 1 || aimiliClient == nil || xuiClient == nil || groups == nil || accounts == nil {
		return nil, errors.New("maintenance dependencies are required")
	}
	lifetime := config.LifetimeContext
	if lifetime == nil {
		lifetime = context.Background()
	}
	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	pollTimeout := config.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 10 * time.Minute
	}
	return &Service{
		config: config, aimili: aimiliClient, xui: xuiClient, groups: groups, accounts: accounts,
		lifetime: lifetime, pollInterval: pollInterval, pollTimeout: pollTimeout, reconcile: config.Reconcile,
	}, nil
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
	maxOnline := service.config.MaxOnline
	if source, ok := service.aimili.(capacitySource); ok {
		if live, liveErr := source.Capacity(ctx); liveErr == nil && live.RegularExitSlots > 0 {
			maxOnline = live.RegularExitSlots
		}
	}
	return Summary{AccountSyncStatus: state.Status, CandidateCount: len(candidates), OnlineCount: len(groups), MaxOnline: maxOnline}, nil
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

func (service *Service) Capacity(ctx context.Context) (CapacitySummary, error) {
	source, ok := service.aimili.(capacitySource)
	if !ok {
		return CapacitySummary{}, &Error{Code: "not_configured"}
	}
	result, err := source.Capacity(ctx)
	if err != nil {
		return CapacitySummary{}, &Error{Code: "service_unavailable"}
	}
	return result, nil
}

func (service *Service) UpdateCapacity(ctx context.Context, update aimili.CapacityUpdate) (CapacitySummary, error) {
	source, ok := service.aimili.(capacitySource)
	if !ok {
		return CapacitySummary{}, &Error{Code: "not_configured"}
	}
	var previous aimili.Capacity
	var err error
	var setter interface{ SetCapacity(int) error }
	if update.RegularExitSlots != nil {
		checker, ok := service.groups.(interface{ CanSetCapacity(int) error })
		if !ok {
			return CapacitySummary{}, &Error{Code: "not_configured"}
		}
		setter, ok = service.groups.(interface{ SetCapacity(int) error })
		if !ok {
			return CapacitySummary{}, &Error{Code: "not_configured"}
		}
		if err := checker.CanSetCapacity(*update.RegularExitSlots); err != nil {
			return CapacitySummary{}, &Error{Code: "capacity_below_active"}
		}
		previous, err = source.Capacity(ctx)
		if err != nil {
			return CapacitySummary{}, &Error{Code: "service_unavailable"}
		}
	}
	result, err := source.UpdateCapacity(ctx, update)
	if err != nil {
		var adapterError *aimili.AdapterError
		if errors.As(err, &adapterError) {
			switch adapterError.Code {
			case "capacity_limit_exceeded", "capacity_upgrade_required", "capacity_storage_failed", "invalid_capacity", "operation_busy":
				return CapacitySummary{}, &Error{Code: adapterError.Code}
			}
		}
		return CapacitySummary{}, &Error{Code: "capacity_update_failed"}
	}
	if update.RegularExitSlots != nil {
		if err := setter.SetCapacity(result.RegularExitSlots); err != nil {
			if _, rollbackErr := source.UpdateCapacity(ctx, aimili.CapacityUpdate{
				RegularExitSlots:     &previous.RegularExitSlots,
				TargetValidNodeCount: &previous.TargetValidNodeCount,
				MaxValidNodeCount:    &previous.MaxValidNodeCount,
			}); rollbackErr != nil {
				return CapacitySummary{}, &Error{Code: "repair_required"}
			}
			return CapacitySummary{}, &Error{Code: "capacity_update_failed"}
		}
		if result.RegularExitSlots > previous.RegularExitSlots && service.reconcile != nil {
			go service.reconcileExpandedCapacity(result.RegularExitSlots)
		}
	}
	return result, nil
}

func (service *Service) reconcileExpandedCapacity(slots int) {
	source, ok := service.aimili.(capacitySource)
	if !ok {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()
	lastReady := -1
	for {
		ctx, cancel := context.WithTimeout(service.lifetime, 8*time.Second)
		current, err := source.Capacity(ctx)
		cancel()
		if err == nil {
			if current.RegularExitSlots != slots {
				return
			}
			if current.ReadyRegularExitSlots > lastReady {
				lastReady = current.ReadyRegularExitSlots
				ctx, cancel := context.WithTimeout(service.lifetime, service.pollTimeout)
				service.reconcile(ctx)
				cancel()
			}
			if current.ReadyRegularExitSlots >= slots {
				return
			}
		}
		select {
		case <-service.lifetime.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (service *Service) RefreshAimiliVPN(ctx context.Context) (AimiliSummary, error) {
	return AimiliSummary{}, &Error{Code: "country_required"}
}

func (service *Service) CandidateCountries(ctx context.Context) ([]aimili.CandidateCountry, error) {
	countries, err := service.aimili.CandidateCountries(ctx)
	if err != nil {
		return nil, countryRefreshError(err)
	}
	// Older AimiliVPN control planes expose only the per-country catalog
	// count.  Keep the UI aggregate counters correct by deriving the missing
	// fields from the same current candidate snapshot instead of displaying
	// zeroes until AimiliVPN is upgraded.
	candidates, candidateErr := service.availableCandidates(ctx)
	if candidateErr != nil {
		return nil, candidateErr
	}
	officialTotal := 0
	for _, country := range countries {
		officialTotal += country.CandidateCount
	}
	validCountries := make(map[string]struct{})
	for _, candidate := range candidates {
		validCountries[strings.ToUpper(strings.TrimSpace(candidate.CountryCode))] = struct{}{}
	}
	for index := range countries {
		if countries[index].OfficialCandidateTotal <= 0 {
			countries[index].OfficialCandidateTotal = officialTotal
		}
		if countries[index].ValidNodeCount <= 0 {
			countries[index].ValidNodeCount = len(candidates)
		}
		if countries[index].ValidCountryCount <= 0 {
			countries[index].ValidCountryCount = len(validCountries)
		}
	}
	return countries, nil
}

func (service *Service) StartAimiliVPNRefresh(ctx context.Context, country string) (aimili.CountryRefresh, error) {
	refresh, err := service.aimili.StartCountryRefresh(ctx, country)
	if err != nil {
		return aimili.CountryRefresh{}, countryRefreshError(err)
	}
	if refresh.State == "running" && service.reconcile != nil {
		go service.pollAimiliVPNRefresh()
	}
	return refresh, nil
}

func (service *Service) AimiliVPNRefresh(ctx context.Context) (aimili.CountryRefresh, error) {
	refresh, err := service.aimili.CountryRefresh(ctx)
	if err != nil {
		return aimili.CountryRefresh{}, countryRefreshError(err)
	}
	return refresh, nil
}

func (service *Service) DedicatedStandbys(ctx context.Context) ([]aimili.DedicatedStandby, error) {
	source, ok := service.aimili.(dedicatedStandbySource)
	if !ok {
		return nil, &Error{Code: "not_configured"}
	}
	result, err := source.DedicatedStandbys(ctx)
	if err != nil {
		return nil, standbyError(err)
	}
	return result, nil
}

func (service *Service) ConfigureDedicatedStandbys(ctx context.Context, configs []aimili.DedicatedStandbyConfig) ([]aimili.DedicatedStandby, error) {
	source, ok := service.aimili.(dedicatedStandbySource)
	if !ok {
		return nil, &Error{Code: "not_configured"}
	}
	result, err := source.ConfigureDedicatedStandbys(ctx, configs)
	if err != nil {
		return nil, standbyError(err)
	}
	return result, nil
}

func (service *Service) AssignDedicatedStandby(ctx context.Context, index int, candidateID string) (aimili.DedicatedStandby, error) {
	source, ok := service.aimili.(dedicatedStandbySource)
	if !ok {
		return aimili.DedicatedStandby{}, &Error{Code: "not_configured"}
	}
	result, err := source.AssignDedicatedStandby(ctx, index, candidateID)
	if err != nil {
		return aimili.DedicatedStandby{}, standbyError(err)
	}
	return result, nil
}

func standbyError(err error) error {
	var adapterError *aimili.AdapterError
	if errors.As(err, &adapterError) {
		switch adapterError.Code {
		case "invalid_request", "slot_not_found", "standby_disabled", "candidate_unavailable", "candidate_in_use", "operation_busy":
			return &Error{Code: adapterError.Code}
		}
	}
	return &Error{Code: "service_unavailable"}
}

func (service *Service) pollAimiliVPNRefresh() {
	ctx, cancel := context.WithTimeout(service.lifetime, service.pollTimeout)
	defer cancel()
	ticker := time.NewTicker(service.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh, err := service.aimili.CountryRefresh(ctx)
			if err != nil {
				return
			}
			switch refresh.State {
			case "completed":
				service.reconcile(ctx)
				return
			case "failed", "idle":
				return
			}
		}
	}
}

func countryRefreshError(err error) error {
	var adapterError *aimili.AdapterError
	if errors.As(err, &adapterError) {
		switch adapterError.Code {
		case "maintenance_busy":
			return &Error{Code: "maintenance_busy"}
		case "invalid_request", "invalid_country":
			return &Error{Code: "invalid_request"}
		}
	}
	return &Error{Code: "service_unavailable"}
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
	if public, ok := inboundByTag(snapshot.Inbounds, "aimili-reality"); ok && managedPublicProtocol(public.Protocol) {
		result.ManagedPublicCount++
		if public.Protocol == "vless" {
			result.ManagedVLESSCount++
		}
	} else {
		result.OwnershipMatches = false
	}
	if mixed, ok := inboundByTag(snapshot.Inbounds, "agw-main-mixed"); ok && mixed.Protocol == "mixed" {
		result.ManagedMixedCount++
	} else {
		result.OwnershipMatches = false
	}
	if outbound, ok := outbounds["aimili-socks"]; ok && outbound.Protocol == "socks" {
		result.ManagedOutboundCount++
	} else {
		result.OwnershipMatches = false
	}
	for _, group := range groups {
		vless, vlessOK := inbounds[group.PublicInboundID]
		if vlessOK && vless.Tag == group.ResourceName+"-vless" && managedPublicProtocol(vless.Protocol) {
			result.ManagedPublicCount++
			if vless.Protocol == "vless" {
				result.ManagedVLESSCount++
			}
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

func inboundByTag(inbounds []xui.Inbound, tag string) (xui.Inbound, bool) {
	var result xui.Inbound
	found := false
	for _, inbound := range inbounds {
		if inbound.Tag != tag {
			continue
		}
		if found {
			return xui.Inbound{}, false
		}
		result, found = inbound, true
	}
	return result, found
}

func managedPublicProtocol(protocol string) bool {
	return protocol == "vless" || protocol == "hysteria"
}

func (service *Service) CheckXUI(ctx context.Context) (XUISummary, error) { return service.XUI(ctx) }

func (service *Service) RepairXUI(ctx context.Context) (XUISummary, error) {
	if err := service.groups.RepairManaged(ctx); err != nil {
		return XUISummary{}, &Error{Code: "repair_failed"}
	}
	summary, err := service.XUI(ctx)
	if err != nil {
		return XUISummary{}, err
	}
	if !summary.OwnershipMatches {
		return XUISummary{}, &Error{Code: "repair_failed"}
	}
	return summary, nil
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
