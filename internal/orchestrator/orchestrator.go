package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/protocoltxn"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

const (
	credentialVLESSClientID = "vless-client-id"
	credentialMixedUsername = "mixed-username"
	credentialMixedPassword = "mixed-password"
)

type Config struct {
	MaxGroups           int
	VLESSPortStart      int
	VLESSPortEnd        int
	MixedPortStart      int
	MixedPortEnd        int
	AggregateVLESSPort  int
	MainMixedPort       int
	PublicHost          string
	XrayPath            string
	ProbeHost           string
	ReadyTimeout        time.Duration
	PollInterval        time.Duration
	Now                 func() time.Time
	ProtocolTransaction protocolTransactionClient
}

type EnableRequest struct {
	CountryCode        string
	ProxyType          domain.ProxyType
	CandidateID        string
	CandidateIP        string
	CandidateLatencyMS int
}

type Country struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	ResidentialCount int    `json:"residentialCount"`
	DatacenterCount  int    `json:"datacenterCount"`
}

type Connections struct {
	ProtocolMode domain.ProtocolMode `json:"protocolMode"`
	PublicURI    string              `json:"publicUri"`
	VLESSURI     string              `json:"vlessUri,omitempty"`
	VLESSError   string              `json:"vlessError,omitempty"`
	SOCKS5HURI   string              `json:"socks5hUri"`
}

type SubscriptionResult struct {
	URL            string              `json:"url"`
	InboundCount   int                 `json:"inboundCount"`
	UpdatedAt      time.Time           `json:"updatedAt"`
	PublicProfiles []xui.PublicProfile `json:"-"`
}

type Error struct{ Code string }

func (e *Error) Error() string { return "proxy group operation failed: " + e.Code }

type groupStore interface {
	CreateProxyGroup(context.Context, domain.ProxyGroup) error
	GetProxyGroup(context.Context, string) (domain.ProxyGroup, error)
	ListProxyGroups(context.Context) ([]domain.ProxyGroup, error)
	UpdateProxyGroup(context.Context, domain.ProxyGroup, int64) error
	DeleteProxyGroup(context.Context, string) error
	GetCredential(context.Context, string, []byte) ([]byte, error)
	ListMixedCIDRs(context.Context) ([]netip.Prefix, error)
	ReplaceMixedCIDRs(context.Context, []netip.Prefix) error
	GetMixedSourcePolicy(context.Context) (store.MixedSourcePolicy, error)
	ReplaceMixedSourcePolicy(context.Context, store.MixedSourcePolicy) error
	SaveMainEgress(context.Context, store.MainEgress) error
	GetAggregateConfig(context.Context) (store.AggregateConfig, error)
	SaveAggregateConfig(context.Context, store.AggregateConfig) error
}

type aimiliClient interface {
	Candidates(context.Context) ([]aimili.Candidate, error)
	CreateSlot(context.Context, aimili.CreateSlotRequest) (aimili.Slot, error)
	ListSlots(context.Context) ([]aimili.Slot, error)
	CheckSlot(context.Context, int) (aimili.SlotCheck, error)
	RotateSlot(context.Context, int) (aimili.Slot, error)
	DeleteSlot(context.Context, int) error
	MainStatus(context.Context) (aimili.MainStatus, error)
	AcquireMutationLease(context.Context, string) (aimili.MutationLease, error)
	RenewMutationLease(context.Context, string) (aimili.MutationLease, error)
	ReleaseMutationLease(context.Context, string) error
}

type xuiClient interface {
	EnsureManagedGroup(context.Context, xui.DesiredGroup) (xui.ManagedGroup, error)
	UpdateManagedGroup(context.Context, xui.DesiredGroup, xui.ManagedGroup) (xui.ManagedGroup, error)
	DeleteManagedGroup(context.Context, xui.ManagedGroup) error
}

type subscriptionXUIClient interface {
	Snapshot(context.Context) (xui.Snapshot, error)
	EnsureSubscriptionClient(context.Context, xui.SubscriptionDesired) (xui.Subscription, error)
	VerifySubscriptionClient(context.Context, xui.SubscriptionDesired) (xui.Subscription, error)
	SubscriptionURL(context.Context, xui.Subscription) (string, error)
}

type legacyAggregateCleanupXUIClient interface {
	subscriptionXUIClient
	DeleteManagedAggregate(context.Context, xui.ManagedAggregate) error
}

type subscriptionStore interface {
	SaveGatewaySubscription(context.Context, store.GatewaySubscription) error
}

type mainEgressStore interface {
	GetMainEgress(context.Context) (store.MainEgress, error)
}

type protocolModeStore interface {
	CreateEgressProtocolMode(context.Context, domain.EgressProtocolMode) error
	GetEgressProtocolMode(context.Context, string) (domain.EgressProtocolMode, error)
	ListEgressProtocolModes(context.Context) ([]domain.EgressProtocolMode, error)
	UpdateEgressProtocolMode(context.Context, domain.EgressProtocolMode, int64) error
}

type protocolTransactionClient interface {
	Apply(context.Context, protocoltxn.Request) (protocoltxn.Result, error)
	Renew(context.Context, string) (protocoltxn.Result, error)
	Finalize(context.Context, string) (protocoltxn.Result, error)
	Rollback(context.Context, string) (protocoltxn.Result, error)
}

type assignAimiliClient interface {
	AssignSlotNode(context.Context, int, aimili.AssignSlotRequest) (aimili.Slot, error)
}

type mainAssignmentAimiliClient interface {
	MainAssignment(context.Context) (aimili.MainAssignmentStatus, error)
	StageMainAssignment(context.Context, aimili.MainAssignmentRequest) (aimili.MainAssignmentStatus, error)
	CommitMainAssignment(context.Context, string) (aimili.MainAssignmentStatus, error)
	RollbackMainAssignment(context.Context, string) (aimili.MainAssignmentStatus, error)
	RepairCommitMainAssignment(context.Context, string) (aimili.MainAssignmentStatus, error)
	RepairReplaceMainAssignment(context.Context, string, aimili.MainRepairRequest) (aimili.MainAssignmentStatus, error)
}

type legacyMainXUIClient interface {
	EnsureLegacyMain(context.Context, xui.LegacyMainDesired) (xui.LegacyMain, error)
}

type proxyValidator interface {
	ValidateSOCKS5H(context.Context, validator.SOCKSTarget) (validator.Result, error)
	ValidateVLESS(context.Context, validator.VLESSTarget) (validator.Result, error)
	ValidatePublic(context.Context, validator.PublicTarget) (validator.Result, error)
}

type Orchestrator struct {
	config                 Config
	store                  groupStore
	aimili                 aimiliClient
	xui                    xuiClient
	validator              proxyValidator
	masterKey              []byte
	locks                  operationLocks
	protocolTransaction    protocolTransactionClient
	mutationLeaseRenewWait func(context.Context, time.Duration) bool
}

func New(config Config, database groupStore, aimiliAdapter aimiliClient, xuiAdapter xuiClient, validation proxyValidator, masterKey []byte) (*Orchestrator, error) {
	if config.MaxGroups < 1 || config.VLESSPortStart < 1 || config.VLESSPortEnd < config.VLESSPortStart ||
		config.MixedPortStart < 1 || config.MixedPortEnd < config.MixedPortStart || net.ParseIP(config.PublicHost) != nil ||
		strings.TrimSpace(config.PublicHost) == "" || strings.TrimSpace(config.XrayPath) == "" || strings.TrimSpace(config.ProbeHost) == "" ||
		database == nil || aimiliAdapter == nil || xuiAdapter == nil || validation == nil || len(masterKey) != 32 {
		return nil, errors.New("invalid orchestrator configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 60 * time.Second
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 2 * time.Second
	}
	if config.AggregateVLESSPort == 0 {
		config.AggregateVLESSPort = config.VLESSPortEnd + 1
	}
	if config.MainMixedPort == 0 {
		config.MainMixedPort = 31000
	}
	if config.MainMixedPort < 1 || config.MainMixedPort > 65535 || (config.MainMixedPort >= config.MixedPortStart && config.MainMixedPort <= config.MixedPortEnd) {
		return nil, errors.New("invalid main mixed port")
	}
	if config.AggregateVLESSPort < 1 || config.AggregateVLESSPort > 65535 || (config.AggregateVLESSPort >= config.VLESSPortStart && config.AggregateVLESSPort <= config.VLESSPortEnd) {
		return nil, errors.New("invalid aggregate VLESS port")
	}
	return &Orchestrator{config: config, store: database, aimili: aimiliAdapter, xui: xuiAdapter, validator: validation, masterKey: append([]byte(nil), masterKey...), protocolTransaction: config.ProtocolTransaction}, nil
}

func (o *Orchestrator) Countries(ctx context.Context) ([]Country, error) {
	candidates, err := o.aimili.Candidates(ctx)
	if err != nil {
		return nil, operationError(err)
	}
	byCode := make(map[string]*Country)
	for _, candidate := range candidates {
		code := strings.ToUpper(candidate.CountryCode)
		if len(code) != 2 || candidate.ProbeStatus != "available" {
			continue
		}
		country := byCode[code]
		if country == nil {
			country = &Country{Code: code, Name: candidate.CountryName}
			byCode[code] = country
		}
		switch candidate.ProxyType {
		case string(domain.ProxyTypeResidential):
			country.ResidentialCount++
		case string(domain.ProxyTypeDatacenter):
			country.DatacenterCount++
		}
	}
	result := make([]Country, 0, len(byCode))
	for _, country := range byCode {
		result = append(result, *country)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result, nil
}

func (o *Orchestrator) List(ctx context.Context) ([]domain.ProxyGroup, error) {
	return o.store.ListProxyGroups(ctx)
}

// Activate switches a standby catalog entry into the bounded live set. At
// capacity, the oldest live group is retired first. A failed switch attempts
// to restore that previous group before returning the original error.
func (o *Orchestrator) Activate(ctx context.Context, id string) (domain.ProxyGroup, error) {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	unlock := o.locks.lock("activation")
	defer unlock()
	candidates, err := o.aimili.Candidates(ctx)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	var target *aimili.Candidate
	for index := range candidates {
		candidate := &candidates[index]
		proxyType := domain.ProxyType(candidate.ProxyType)
		if candidate.ProbeStatus != "available" || !proxyType.Valid() || strings.TrimSpace(candidate.ID) == "" {
			continue
		}
		identity, identityErr := domain.NewProxyGroupIdentity(candidate.CountryCode, proxyType, candidate.ID)
		if identityErr == nil && identity.ID == strings.TrimSpace(id) {
			target = candidate
			break
		}
	}
	if target == nil {
		return domain.ProxyGroup{}, &Error{Code: "not_found"}
	}
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return domain.ProxyGroup{}, &Error{Code: "storage_failed"}
	}
	for _, group := range groups {
		if group.ID == id {
			return group, nil
		}
	}
	var previous *domain.ProxyGroup
	if len(groups) >= o.config.MaxGroups {
		sort.Slice(groups, func(i, j int) bool { return groups[i].UpdatedAt.Before(groups[j].UpdatedAt) })
		copy := groups[0]
		previous = &copy
		if err := o.Disable(ctx, previous.ID); err != nil {
			return domain.ProxyGroup{}, err
		}
	}
	request := EnableRequest{CountryCode: target.CountryCode, ProxyType: domain.ProxyType(target.ProxyType), CandidateID: target.ID, CandidateIP: target.IP, CandidateLatencyMS: target.LatencyMS}
	activated, err := o.Enable(ctx, request)
	if err == nil {
		return activated, nil
	}
	if previous != nil {
		_, _ = o.Enable(ctx, EnableRequest{CountryCode: previous.CountryCode, ProxyType: previous.ProxyType, CandidateID: previous.CandidateID, CandidateIP: previous.CandidateIP, CandidateLatencyMS: previous.CandidateLatencyMS})
	}
	return domain.ProxyGroup{}, err
}

func (o *Orchestrator) Enable(ctx context.Context, request EnableRequest) (domain.ProxyGroup, error) {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	var identity domain.ProxyGroup
	var err error
	if strings.TrimSpace(request.CandidateID) == "" {
		identity, err = domain.NewProxyGroupIdentity(request.CountryCode, request.ProxyType)
	} else {
		identity, err = domain.NewProxyGroupIdentity(request.CountryCode, request.ProxyType, request.CandidateID)
	}
	if err != nil {
		return domain.ProxyGroup{}, &Error{Code: "invalid_request"}
	}
	unlock := o.locks.lock("all")
	defer unlock()
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return domain.ProxyGroup{}, &Error{Code: "storage_failed"}
	}
	if len(groups) >= o.config.MaxGroups {
		return domain.ProxyGroup{}, &Error{Code: "capacity_exceeded"}
	}
	policy, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return domain.ProxyGroup{}, err
	}
	vlessPort, mixedPort, ok := o.allocatePorts(groups)
	if !ok {
		return domain.ProxyGroup{}, &Error{Code: "port_capacity_exceeded"}
	}
	now := o.config.Now().UTC()
	reservedSlot, err := o.reserveAimiliSlot(ctx, groups)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	identity.AimiliSlot = reservedSlot
	identity.CandidateIP = request.CandidateIP
	identity.CandidateLatencyMS = request.CandidateLatencyMS
	identity.LastSeenAt = now
	identity.PublicPort = vlessPort
	identity.MixedPort = mixedPort
	identity.CreatedAt = now
	identity.UpdatedAt = now
	if err := o.store.CreateProxyGroup(ctx, identity); err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	group := identity
	slot, err := o.aimili.CreateSlot(ctx, aimili.CreateSlotRequest{Country: group.CountryCode, ProxyType: string(group.ProxyType), CandidateID: group.CandidateID})
	if err != nil {
		_ = o.store.DeleteProxyGroup(ctx, group.ID)
		return domain.ProxyGroup{}, operationError(err)
	}
	group.AimiliSlot = slot.Number
	group.CountryName = slot.CountryName
	checked, err := o.waitForSlot(ctx, slot.Number)
	if err != nil || !checked.EgressOK || net.ParseIP(checked.ExitIP) == nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, xui.ManagedGroup{}, codeOr(err, "egress_unavailable"))
	}
	checked, err = o.ensureUniqueExit(ctx, group.ID, checked)
	if err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, xui.ManagedGroup{}, errorCode(err))
	}
	group.ExitIP = checked.ExitIP
	group.ExitIPCheckedAt = checked.CheckedAt
	managed, err := o.xui.EnsureManagedGroup(ctx, xui.DesiredGroup{
		ResourceName: group.ResourceName, SOCKSPort: checked.Port, VLESSPort: group.PublicPort, MixedPort: group.MixedPort,
		VLESSClientID: string(credentials.vlessID), MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword),
		MixedSourceRestrictionEnabled: policy.Enabled, MixedSourceCIDRs: prefixStrings(policy.CIDRs),
		RealityTarget: "127.0.0.1:443", RealityServerName: o.config.PublicHost,
	})
	if err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, xui.ManagedGroup{}, errorCode(err))
	}
	group.ConfigFingerprint = managed.Fingerprint
	group.PublicInboundID = managed.VLESSInboundID
	group.MixedInboundID = managed.MixedInboundID
	group.RealityPublicKey = managed.PublicKey
	group.RealityShortID = managed.ShortID
	group.RealityServerName = managed.ServerName
	group.RealityMLDSA65Verify = managed.MLDSA65Verify
	socksResult, err := o.validateSOCKS(ctx, group, credentials)
	if err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, errorCode(err))
	}
	vlessResult, err := o.validateVLESS(ctx, group, credentials)
	if err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, errorCode(err))
	}
	group.SOCKSLatencyMS = durationMillis(socksResult.Latency)
	group.VLESSLatencyMS = durationMillis(vlessResult.Latency)
	group.Status = domain.ProxyGroupReady
	group.LastCheckedAt = o.config.Now().UTC()
	group.LastErrorCode = ""
	group.UpdatedAt = group.LastCheckedAt
	if err := o.save(ctx, &group); err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, "storage_failed")
	}
	protocols, ok := o.store.(protocolModeStore)
	if !ok {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, "storage_failed")
	}
	if err := protocols.CreateEgressProtocolMode(ctx, domain.EgressProtocolMode{
		EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision,
		State: domain.ProtocolReady, Version: 1, UpdatedAt: o.config.Now().UTC(),
	}); err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, "storage_failed")
	}
	_, _ = o.Subscription(ctx)
	return group, nil
}

func (o *Orchestrator) reserveAimiliSlot(ctx context.Context, groups []domain.ProxyGroup) (int, error) {
	slots, err := o.aimili.ListSlots(ctx)
	if err != nil {
		return 0, err
	}
	used := make(map[int]struct{}, len(groups)+len(slots))
	for _, group := range groups {
		used[group.AimiliSlot] = struct{}{}
	}
	for _, slot := range slots {
		used[slot.Number] = struct{}{}
	}
	for number := 0; ; number++ {
		if _, exists := used[number]; !exists {
			return number, nil
		}
	}
}

func durationMillis(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	millis := value.Milliseconds()
	if millis == 0 {
		return 1
	}
	return int(millis)
}

func (o *Orchestrator) Check(ctx context.Context, id string) (domain.ProxyGroup, error) {
	unlock := o.locks.lock(id)
	defer unlock()
	group, err := o.store.GetProxyGroup(ctx, id)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	credentialsErr := error(nil)
	_, credentials, credentialsErr := o.runtimeInputs(ctx)
	if credentialsErr != nil {
		return domain.ProxyGroup{}, credentialsErr
	}
	checked, err := o.aimili.CheckSlot(ctx, group.AimiliSlot)
	if err == nil && checked.EgressOK {
		applySlotSnapshot(&group, checked)
		_, err = o.validateSOCKS(ctx, group, credentials)
		if err == nil {
			_, err = o.validateCurrentPublic(ctx, group)
		}
	}
	group.LastCheckedAt = o.config.Now().UTC()
	group.UpdatedAt = group.LastCheckedAt
	if err != nil || !checked.EgressOK {
		group.Status = domain.ProxyGroupDegraded
		group.LastErrorCode = codeOr(err, "egress_unavailable")
	} else {
		group.Status = domain.ProxyGroupReady
		group.LastErrorCode = ""
	}
	if saveErr := o.save(ctx, &group); saveErr != nil {
		return domain.ProxyGroup{}, saveErr
	}
	if err != nil {
		return group, operationError(err)
	}
	return group, nil
}

func (o *Orchestrator) Rotate(ctx context.Context, id string) (domain.ProxyGroup, error) {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	unlock := o.locks.lock(id)
	defer unlock()
	group, err := o.store.GetProxyGroup(ctx, id)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	group.Status = domain.ProxyGroupRotating
	group.UpdatedAt = o.config.Now().UTC()
	if err = o.save(ctx, &group); err != nil {
		return domain.ProxyGroup{}, err
	}
	_, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return domain.ProxyGroup{}, err
	}
	slot, err := o.aimili.RotateSlot(ctx, group.AimiliSlot)
	if err == nil {
		slot, err = o.waitForSlot(ctx, group.AimiliSlot)
	}
	if err == nil && slot.EgressOK {
		applySlotSnapshot(&group, slot)
		_, err = o.validateSOCKS(ctx, group, credentials)
		if err == nil {
			_, err = o.validateCurrentPublic(ctx, group)
		}
	}
	group.LastRotatedAt = o.config.Now().UTC()
	group.LastCheckedAt = group.LastRotatedAt
	group.UpdatedAt = group.LastRotatedAt
	if err != nil || !slot.EgressOK {
		group.Status = domain.ProxyGroupDegraded
		group.LastErrorCode = codeOr(err, "egress_unavailable")
	} else {
		group.Status = domain.ProxyGroupReady
		group.LastErrorCode = ""
	}
	if saveErr := o.save(ctx, &group); saveErr != nil {
		return domain.ProxyGroup{}, saveErr
	}
	if err != nil {
		return group, operationError(err)
	}
	return group, nil
}

func applySlotSnapshot(group *domain.ProxyGroup, slot aimili.Slot) {
	if group == nil {
		return
	}
	previousCandidateID := group.CandidateID
	if nodeID := strings.TrimSpace(slot.NodeID); nodeID != "" {
		group.CandidateID = nodeID
		if nodeID != previousCandidateID {
			group.ExitIPCheckedAt = 0
		}
	}
	if candidateIP := strings.TrimSpace(slot.CandidateIP); net.ParseIP(candidateIP) != nil {
		group.CandidateIP = candidateIP
	}
	if country := strings.ToUpper(strings.TrimSpace(slot.Country)); len(country) == 2 {
		group.CountryCode = country
	}
	if name := strings.TrimSpace(slot.CountryName); name != "" {
		group.CountryName = name
	}
	if proxyType := domain.ProxyType(strings.ToLower(strings.TrimSpace(slot.ProxyType))); proxyType.Valid() {
		group.ProxyType = proxyType
	}
	if slot.LatencyMS >= 0 {
		group.CandidateLatencyMS = slot.LatencyMS
	}
	if exitIP := strings.TrimSpace(slot.ExitIP); net.ParseIP(exitIP) != nil {
		group.ExitIP = exitIP
		if slot.CheckedAt > 0 {
			group.ExitIPCheckedAt = slot.CheckedAt
		}
	}
}

func (o *Orchestrator) Disable(ctx context.Context, id string) error {
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	unlock := o.locks.lock("all")
	defer unlock()
	group, err := o.store.GetProxyGroup(ctx, id)
	if err != nil {
		return operationError(err)
	}
	group.Status = domain.ProxyGroupDisabling
	group.UpdatedAt = o.config.Now().UTC()
	if err = o.save(ctx, &group); err != nil {
		return err
	}
	managed := managedFromGroup(group)
	xuiErr := o.xui.DeleteManagedGroup(ctx, managed)
	slotErr := o.aimili.DeleteSlot(ctx, group.AimiliSlot)
	if xuiErr != nil || slotErr != nil {
		group.Status = domain.ProxyGroupRepairRequired
		group.LastErrorCode = "disable_failed"
		group.UpdatedAt = o.config.Now().UTC()
		_ = o.save(ctx, &group)
		return &Error{Code: "repair_required"}
	}
	if err = o.store.DeleteProxyGroup(ctx, id); err != nil {
		return &Error{Code: "storage_failed"}
	}
	_, _ = o.Subscription(ctx)
	return nil
}

func (o *Orchestrator) Connections(ctx context.Context, id string) (Connections, error) {
	persistence, ok := o.store.(protocolModeStore)
	if !ok {
		return Connections{}, &Error{Code: "not_configured"}
	}
	state, err := persistence.GetEgressProtocolMode(ctx, id)
	if err != nil || state.State != domain.ProtocolReady {
		return Connections{}, &Error{Code: "not_ready"}
	}
	var group domain.ProxyGroup
	if id == "agw-main" {
		mainStore, ok := o.store.(mainEgressStore)
		if !ok {
			return Connections{}, &Error{Code: "not_configured"}
		}
		main, getErr := mainStore.GetMainEgress(ctx)
		if getErr != nil || !main.Enabled {
			return Connections{}, &Error{Code: "not_ready"}
		}
		status, statusErr := o.aimili.MainStatus(ctx)
		if statusErr != nil || !status.Active || !status.EgressOK || status.Port != 7928 || status.ExitIP != main.ExitIP {
			return Connections{}, &Error{Code: "not_ready"}
		}
		group = mainEgressGroup(main)
	} else {
		group, err = o.store.GetProxyGroup(ctx, id)
		if err != nil {
			return Connections{}, operationError(err)
		}
		if group.Status != domain.ProxyGroupReady {
			return Connections{}, &Error{Code: "not_ready"}
		}
	}
	_, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return Connections{}, err
	}
	subscription, err := o.Subscription(ctx)
	if err != nil {
		return Connections{}, err
	}
	var profile *xui.PublicProfile
	for index := range subscription.PublicProfiles {
		candidate := &subscription.PublicProfiles[index]
		if candidate.InboundID == group.PublicInboundID && candidate.Mode == state.ActiveMode {
			profile = candidate
			break
		}
	}
	if profile == nil {
		return Connections{}, &Error{Code: "subscription_pending"}
	}
	publicURI, err := buildPublicURI(o.config.PublicHost, group.PublicPort, group.ResourceName, *profile)
	if err != nil {
		return Connections{}, err
	}
	socks := url.URL{Scheme: "socks5h", User: url.UserPassword(string(credentials.mixedUsername), string(credentials.mixedPassword)), Host: net.JoinHostPort(o.config.PublicHost, fmt.Sprint(group.MixedPort))}
	result := Connections{ProtocolMode: state.ActiveMode, PublicURI: publicURI, SOCKS5HURI: socks.String()}
	if state.ActiveMode == domain.ProtocolHysteria2QUICTLS {
		result.VLESSError = "protocol_changed"
	} else {
		result.VLESSURI = publicURI
	}
	return result, nil
}

func buildPublicURI(publicHost string, port int, name string, profile xui.PublicProfile) (string, error) {
	if publicHost == "" || port < 1 || port > 65535 || profile.InboundID < 1 || profile.Mode.Valid() == false {
		return "", &Error{Code: "invalid_response"}
	}
	if profile.Mode == domain.ProtocolHysteria2QUICTLS {
		if profile.Auth == "" {
			return "", &Error{Code: "invalid_response"}
		}
		uri := url.URL{Scheme: "hysteria2", User: url.User(profile.Auth), Host: net.JoinHostPort(publicHost, fmt.Sprint(port)), Path: "/", Fragment: name}
		query := uri.Query()
		query.Set("sni", publicHost)
		query.Set("insecure", "0")
		uri.RawQuery = query.Encode()
		return uri.String(), nil
	}
	if profile.ClientID == "" || profile.PublicKey == "" || profile.ShortID == "" || profile.ServerName == "" {
		return "", &Error{Code: "invalid_response"}
	}
	uri := url.URL{Scheme: "vless", User: url.User(profile.ClientID), Host: net.JoinHostPort(publicHost, fmt.Sprint(port)), Fragment: name}
	query := uri.Query()
	query.Set("encryption", "none")
	query.Set("security", "reality")
	query.Set("sni", profile.ServerName)
	query.Set("fp", "chrome")
	query.Set("pbk", profile.PublicKey)
	query.Set("sid", profile.ShortID)
	if profile.MLDSA65Verify != "" {
		query.Set("pqv", profile.MLDSA65Verify)
	}
	if profile.Mode == domain.ProtocolVLESSXHTTPReality {
		if profile.XHTTPPath == "" {
			return "", &Error{Code: "invalid_response"}
		}
		query.Set("type", "xhttp")
		query.Set("path", profile.XHTTPPath)
		query.Set("mode", "auto")
	} else {
		query.Set("type", "tcp")
		query.Set("flow", "xtls-rprx-vision")
	}
	uri.RawQuery = query.Encode()
	return uri.String(), nil
}

func (o *Orchestrator) mainConnections(ctx context.Context) (Connections, error) {
	status, err := o.aimili.MainStatus(ctx)
	if err != nil {
		return Connections{}, operationError(err)
	}
	if !status.Active || !status.EgressOK || status.Port != 7928 {
		return Connections{}, &Error{Code: "not_ready"}
	}
	manager, ok := o.xui.(legacyMainXUIClient)
	if !ok {
		return Connections{}, &Error{Code: "not_configured"}
	}
	policy, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return Connections{}, err
	}
	legacy, err := manager.EnsureLegacyMain(ctx, xui.LegacyMainDesired{VLESSPort: 8443, MixedPort: o.config.MainMixedPort, SOCKSPort: 7928, MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword), MixedSourceRestrictionEnabled: policy.Enabled, MixedSourceCIDRs: prefixStrings(policy.CIDRs), RealityTarget: "127.0.0.1:443", RealityServerName: o.config.PublicHost})
	if err != nil {
		return Connections{}, operationError(err)
	}
	proxyType := domain.ProxyType(status.ProxyType)
	if !proxyType.Valid() {
		proxyType = domain.ProxyTypeDatacenter
	}
	country := strings.ToUpper(strings.TrimSpace(status.Country))
	if len(country) != 2 {
		country = "ZZ"
	}
	if err := o.store.SaveMainEgress(ctx, store.MainEgress{ResourceName: "agw-main", CountryCode: country, CountryName: status.CountryName, ProxyType: proxyType, CandidateID: status.CandidateID, ExitIP: status.ExitIP, PublicInboundID: legacy.VLESSInboundID, MixedInboundID: legacy.MixedInboundID, PublicPort: legacy.VLESSPort, MixedPort: legacy.MixedPort, Enabled: true, UpdatedAt: o.config.Now().UTC()}); err != nil {
		return Connections{}, &Error{Code: "storage_failed"}
	}
	vless := url.URL{Scheme: "vless", User: url.User(legacy.ClientID), Host: net.JoinHostPort(o.config.PublicHost, fmt.Sprint(legacy.VLESSPort)), Fragment: "aimili-main"}
	query := vless.Query()
	query.Set("encryption", "none")
	query.Set("flow", "xtls-rprx-vision")
	query.Set("security", "reality")
	query.Set("sni", legacy.ServerName)
	query.Set("fp", "chrome")
	query.Set("pbk", legacy.PublicKey)
	query.Set("sid", legacy.ShortID)
	query.Set("type", "tcp")
	vless.RawQuery = query.Encode()
	socks := url.URL{Scheme: "socks5h", User: url.UserPassword(string(credentials.mixedUsername), string(credentials.mixedPassword)), Host: net.JoinHostPort(o.config.PublicHost, fmt.Sprint(legacy.MixedPort))}
	return Connections{VLESSURI: vless.String(), SOCKS5HURI: socks.String()}, nil
}

type runtimeCredentials struct{ vlessID, mixedUsername, mixedPassword []byte }

func (o *Orchestrator) runtimeInputs(ctx context.Context) (store.MixedSourcePolicy, runtimeCredentials, error) {
	policy, err := o.store.GetMixedSourcePolicy(ctx)
	if err != nil {
		return store.MixedSourcePolicy{}, runtimeCredentials{}, &Error{Code: "storage_failed"}
	}
	if policy.Enabled && len(policy.CIDRs) == 0 {
		return store.MixedSourcePolicy{}, runtimeCredentials{}, &Error{Code: "mixed_cidr_required"}
	}
	result, err := o.runtimeCredentials(ctx)
	return policy, result, err
}

func (o *Orchestrator) runtimeCredentials(ctx context.Context) (runtimeCredentials, error) {
	var result runtimeCredentials
	for _, item := range []struct {
		purpose string
		target  *[]byte
	}{{credentialVLESSClientID, &result.vlessID}, {credentialMixedUsername, &result.mixedUsername}, {credentialMixedPassword, &result.mixedPassword}} {
		value, getErr := o.store.GetCredential(ctx, item.purpose, o.masterKey)
		if getErr != nil {
			return runtimeCredentials{}, &Error{Code: "credentials_not_configured"}
		}
		*item.target = value
	}
	return result, nil
}
func (o *Orchestrator) allocatePorts(groups []domain.ProxyGroup) (int, int, bool) {
	usedV := map[int]bool{}
	usedM := map[int]bool{}
	for _, g := range groups {
		usedV[g.PublicPort] = true
		usedM[g.MixedPort] = true
	}
	v, m := 0, 0
	for p := o.config.VLESSPortStart; p <= o.config.VLESSPortEnd; p++ {
		if !usedV[p] {
			v = p
			break
		}
	}
	for p := o.config.MixedPortStart; p <= o.config.MixedPortEnd; p++ {
		if !usedM[p] {
			m = p
			break
		}
	}
	return v, m, v > 0 && m > 0
}
func (o *Orchestrator) validateSOCKS(ctx context.Context, g domain.ProxyGroup, c runtimeCredentials) (validator.Result, error) {
	return o.validator.ValidateSOCKS5H(ctx, validator.SOCKSTarget{Address: net.JoinHostPort("127.0.0.1", fmt.Sprint(g.MixedPort)), Username: string(c.mixedUsername), Password: string(c.mixedPassword), ProbeHost: o.config.ProbeHost, ExpectedExitIP: g.ExitIP})
}
func (o *Orchestrator) validateVLESS(ctx context.Context, g domain.ProxyGroup, c runtimeCredentials) (validator.Result, error) {
	return o.validator.ValidateVLESS(ctx, validator.VLESSTarget{XrayPath: o.config.XrayPath, InboundAddress: net.JoinHostPort("127.0.0.1", fmt.Sprint(g.PublicPort)), ClientID: string(c.vlessID), PublicKey: g.RealityPublicKey, ShortID: g.RealityShortID, ServerName: g.RealityServerName, ProbeHost: o.config.ProbeHost, ExpectedExitIP: g.ExitIP, MLDSA65Verify: g.RealityMLDSA65Verify})
}

func (o *Orchestrator) validateCurrentPublic(ctx context.Context, group domain.ProxyGroup) (validator.Result, error) {
	persistence, ok := o.store.(protocolModeStore)
	if !ok {
		return validator.Result{}, &Error{Code: "not_configured"}
	}
	state, err := persistence.GetEgressProtocolMode(ctx, group.ID)
	if err != nil || state.State != domain.ProtocolReady || !state.ActiveMode.Valid() {
		return validator.Result{}, &Error{Code: "not_ready"}
	}
	subscription, err := o.Subscription(ctx)
	if err != nil {
		return validator.Result{}, err
	}
	for index := range subscription.PublicProfiles {
		profile := subscription.PublicProfiles[index]
		if profile.InboundID != group.PublicInboundID || profile.Mode != state.ActiveMode {
			continue
		}
		return o.validator.ValidatePublic(ctx, validator.PublicTarget{
			Mode: state.ActiveMode, XrayPath: o.config.XrayPath, InboundAddress: net.JoinHostPort("127.0.0.1", fmt.Sprint(group.PublicPort)),
			ClientID: profile.ClientID, Auth: profile.Auth, PublicKey: profile.PublicKey, ShortID: profile.ShortID,
			ServerName: profile.ServerName, MLDSA65Verify: profile.MLDSA65Verify, XHTTPPath: profile.XHTTPPath,
			TLSServerName: o.config.PublicHost, ProbeHost: o.config.ProbeHost, ExpectedExitIP: group.ExitIP,
		})
	}
	return validator.Result{}, &Error{Code: "subscription_incomplete"}
}

func (o *Orchestrator) waitForSlot(ctx context.Context, slot int) (aimili.SlotCheck, error) {
	waitContext, cancel := context.WithTimeout(ctx, o.config.ReadyTimeout)
	defer cancel()
	for {
		checked, err := o.aimili.CheckSlot(waitContext, slot)
		if err == nil && checked.EgressOK && net.ParseIP(checked.ExitIP) != nil {
			return checked, nil
		}
		if err != nil && waitContext.Err() == nil {
			return aimili.SlotCheck{}, err
		}
		timer := time.NewTimer(o.config.PollInterval)
		select {
		case <-waitContext.Done():
			timer.Stop()
			return aimili.SlotCheck{}, &Error{Code: "egress_unavailable"}
		case <-timer.C:
		}
	}
}

func (o *Orchestrator) readyExitIPs(ctx context.Context, exceptID string) (map[string]struct{}, error) {
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return nil, &Error{Code: "storage_failed"}
	}
	result := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if group.ID == exceptID || group.Status != domain.ProxyGroupReady {
			continue
		}
		if normalized, ok := normalizeExitIP(group.ExitIP); ok {
			result[normalized] = struct{}{}
		}
	}
	return result, nil
}

func (o *Orchestrator) ensureUniqueExit(ctx context.Context, groupID string, slot aimili.Slot) (aimili.Slot, error) {
	for rotations := 0; ; rotations++ {
		normalized, valid := normalizeExitIP(slot.ExitIP)
		if !slot.EgressOK || !valid {
			return aimili.Slot{}, &Error{Code: "egress_unavailable"}
		}
		existing, err := o.readyExitIPs(ctx, groupID)
		if err != nil {
			return aimili.Slot{}, err
		}
		if _, duplicate := existing[normalized]; !duplicate {
			slot.ExitIP = normalized
			return slot, nil
		}
		if rotations >= 3 {
			return aimili.Slot{}, &Error{Code: "duplicate_exit_ip"}
		}
		if _, err := o.aimili.RotateSlot(ctx, slot.Number); err != nil {
			return aimili.Slot{}, operationError(err)
		}
		slot, err = o.waitForSlot(ctx, slot.Number)
		if err != nil {
			return aimili.Slot{}, err
		}
	}
}

func normalizeExitIP(raw string) (string, bool) {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return "", false
	}
	return ip.String(), true
}

func (o *Orchestrator) save(ctx context.Context, g *domain.ProxyGroup) error {
	expected := g.Version
	if err := o.store.UpdateProxyGroup(ctx, *g, expected); err != nil {
		return operationError(err)
	}
	g.Version++
	return nil
}
func (o *Orchestrator) rollbackEnable(ctx context.Context, g *domain.ProxyGroup, managed xui.ManagedGroup, cause string) error {
	rollbackFailed := false
	if managed.ResourceName != "" {
		if err := o.xui.DeleteManagedGroup(ctx, managed); err != nil {
			rollbackFailed = true
		}
	}
	if err := o.aimili.DeleteSlot(ctx, g.AimiliSlot); err != nil {
		rollbackFailed = true
	}
	if !rollbackFailed {
		if err := o.store.DeleteProxyGroup(ctx, g.ID); err == nil {
			return &Error{Code: cause}
		}
	}
	g.Status = domain.ProxyGroupRepairRequired
	g.LastErrorCode = cause
	g.RecoveryState = "compensation_failed"
	g.UpdatedAt = o.config.Now().UTC()
	_ = o.save(ctx, g)
	return &Error{Code: "repair_required"}
}
func managedFromGroup(g domain.ProxyGroup) xui.ManagedGroup {
	return xui.ManagedGroup{ResourceName: g.ResourceName, VLESSInboundID: g.PublicInboundID, MixedInboundID: g.MixedInboundID, VLESSInboundTag: g.ResourceName + "-vless", MixedInboundTag: g.ResourceName + "-mixed", OutboundTag: g.ResourceName + "-socks", Fingerprint: g.ConfigFingerprint, PublicKey: g.RealityPublicKey, ShortID: g.RealityShortID, ServerName: g.RealityServerName, MLDSA65Verify: g.RealityMLDSA65Verify}
}
func prefixStrings(values []netip.Prefix) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.String()
	}
	return result
}
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var orchestratorError *Error
	if errors.As(err, &orchestratorError) {
		return orchestratorError.Code
	}
	var validationError *validator.Error
	if errors.As(err, &validationError) {
		return validationError.Code
	}
	var aimiliError *aimili.AdapterError
	if errors.As(err, &aimiliError) {
		return aimiliError.Code
	}
	var xuiError *xui.AdapterError
	if errors.As(err, &xuiError) {
		return xuiError.Code
	}
	if errors.Is(err, store.ErrProxyGroupExists) {
		return "already_exists"
	}
	if errors.Is(err, store.ErrProxyGroupNotFound) {
		return "not_found"
	}
	return "operation_failed"
}
func operationError(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: errorCode(err)}
}
func codeOr(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	code := errorCode(err)
	if code == "operation_failed" {
		return fallback
	}
	return code
}
