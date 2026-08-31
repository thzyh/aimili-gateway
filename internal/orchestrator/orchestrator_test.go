package orchestrator

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

func TestEnableCreatesAndValidatesOneStableProxyGroup(t *testing.T) {
	fixture := newFixture()
	orchestrator := fixture.orchestrator(t)
	group, err := orchestrator.Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter})
	if err != nil {
		t.Fatal(err)
	}
	if group.Status != domain.ProxyGroupReady || group.AimiliSlot != 0 || group.ExitIP != "203.0.113.7" ||
		group.PublicPort != 20000 || group.MixedPort != 30000 || group.PublicInboundID == 0 || group.MixedInboundID == 0 {
		t.Fatalf("unexpected ready group: %#v", group)
	}
	want := []string{"slot.create", "slot.check", "xui.ensure", "validate.socks", "validate.vless"}
	if !equalStrings(fixture.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fixture.calls, want)
	}
	if fixture.xui.desired.RealityTarget != "127.0.0.1:443" || fixture.xui.desired.RealityServerName != "proxy.example.test" {
		t.Fatalf("Reality target = %#v", fixture.xui.desired)
	}
	protocol, ok := fixture.store.protocolModes[group.ID]
	if !ok || protocol.ActiveMode != domain.ProtocolVLESSTCPRealityVision || protocol.DesiredMode != domain.ProtocolVLESSTCPRealityVision || protocol.State != domain.ProtocolReady {
		t.Fatalf("default protocol state = %#v, present=%v", protocol, ok)
	}
}

func TestEnableCompensatesWhenDefaultProtocolStateCannotBeCreated(t *testing.T) {
	fixture := newFixture()
	fixture.store.protocolCreateError = errors.New("storage unavailable")
	_, err := fixture.orchestrator(t).Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter})
	if codeOf(err) != "storage_failed" {
		t.Fatalf("error = %v", err)
	}
	if len(fixture.store.groups) != 0 || len(fixture.store.protocolModes) != 0 || !contains(fixture.calls, "xui.delete") || !contains(fixture.calls, "slot.delete") {
		t.Fatalf("incomplete compensation: groups=%#v protocols=%#v calls=%#v", fixture.store.groups, fixture.store.protocolModes, fixture.calls)
	}
}

func TestEnableWaitsForAimiliSlotToCarryRealTraffic(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.unreadyChecks = 2
	_, err := fixture.orchestrator(t).Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter})
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	for _, call := range fixture.calls {
		if call == "slot.check" {
			checks++
		}
	}
	if checks != 3 {
		t.Fatalf("slot checks=%d calls=%#v", checks, fixture.calls)
	}
}

func TestEnableReservesAFreeAimiliSlotBeforePersistingSecondGroup(t *testing.T) {
	fixture := newFixture()
	fixture.store.enforceUniqueSlots = true
	existing, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "jp-existing")
	existing.Status = domain.ProxyGroupReady
	existing.AimiliSlot = 0
	existing.PublicPort = 20000
	existing.MixedPort = 30000
	existing.ExitIP = "203.0.113.7"
	fixture.store.groups[existing.ID] = existing
	fixture.aimili.createdSlots = map[int]aimili.Slot{0: {Number: 0, Country: "JP", ProxyType: "datacenter", EgressOK: true}}
	fixture.aimili.slotsByCandidate = map[string]aimili.Slot{
		"kr-new": {Number: 1, Country: "KR", CountryName: "韩国", ProxyType: "datacenter", Port: 17931, Status: "up", ExitIP: "203.0.113.8", EgressOK: true},
	}

	created, err := fixture.orchestratorWithMax(t, 2).Enable(context.Background(), EnableRequest{
		CountryCode: "KR", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "kr-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.AimiliSlot != 1 || len(fixture.store.groups) != 2 {
		t.Fatalf("created=%#v groups=%#v", created, fixture.store.groups)
	}
}

func TestEnableRotatesANewSlotUntilItsExitIsUnique(t *testing.T) {
	fixture := newFixture()
	existing, _ := domain.NewProxyGroupIdentity("US", domain.ProxyTypeDatacenter, "existing")
	existing.Status = domain.ProxyGroupReady
	existing.ExitIP = "203.0.113.7"
	existing.PublicPort = 20000
	existing.MixedPort = 30000
	existing.CreatedAt = fixture.now().Add(-time.Hour)
	fixture.store.groups[existing.ID] = existing
	fixture.aimili.rotatedExitIPs = []string{"203.0.113.7", "203.0.113.8"}

	created, err := fixture.orchestratorWithMax(t, 2).Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "new-candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ExitIP != "203.0.113.8" {
		t.Fatalf("new group exit = %q", created.ExitIP)
	}
	rotations := 0
	for _, call := range fixture.calls {
		if call == "slot.rotate" {
			rotations++
		}
	}
	if rotations != 2 || fixture.store.groups[existing.ID].ExitIP != "203.0.113.7" {
		t.Fatalf("rotations=%d existing=%#v calls=%#v", rotations, fixture.store.groups[existing.ID], fixture.calls)
	}
}

func TestEnableRollsBackAfterThreeDuplicateExitRotations(t *testing.T) {
	fixture := newFixture()
	existing, _ := domain.NewProxyGroupIdentity("US", domain.ProxyTypeDatacenter, "existing")
	existing.Status = domain.ProxyGroupReady
	existing.ExitIP = "203.0.113.7"
	existing.PublicPort = 20000
	existing.MixedPort = 30000
	fixture.store.groups[existing.ID] = existing
	fixture.aimili.rotatedExitIPs = []string{"203.0.113.7", "203.0.113.7", "203.0.113.7"}

	_, err := fixture.orchestratorWithMax(t, 2).Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "duplicate-candidate"})
	if codeOf(err) != "duplicate_exit_ip" {
		t.Fatalf("error = %v", err)
	}
	rotations := 0
	for _, call := range fixture.calls {
		if call == "slot.rotate" {
			rotations++
		}
	}
	if rotations != 3 || !contains(fixture.calls, "slot.delete") || contains(fixture.calls, "xui.ensure") {
		t.Fatalf("calls = %#v", fixture.calls)
	}
	if len(fixture.store.groups) != 1 || fixture.store.groups[existing.ID].Status != domain.ProxyGroupReady {
		t.Fatalf("rollback changed existing groups: %#v", fixture.store.groups)
	}
}

func TestReadyExitIPsNormalizesAddressesAndExcludesOneGroup(t *testing.T) {
	fixture := newFixture()
	first, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "first")
	first.Status = domain.ProxyGroupReady
	first.ExitIP = "2001:0db8:0:0:0:0:0:1"
	second, _ := domain.NewProxyGroupIdentity("US", domain.ProxyTypeDatacenter, "second")
	second.Status = domain.ProxyGroupDegraded
	second.ExitIP = "203.0.113.9"
	fixture.store.groups[first.ID] = first
	fixture.store.groups[second.ID] = second

	exits, err := fixture.orchestratorWithMax(t, 3).readyExitIPs(context.Background(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(exits) != 1 {
		t.Fatalf("exits = %#v", exits)
	}
	if _, ok := exits["2001:db8::1"]; !ok {
		t.Fatalf("IPv6 exit was not normalized: %#v", exits)
	}
}

func TestPoolIncludesHealthyLegacyMainAsFourthEgress(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainStatus = aimili.MainStatus{Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true}
	pool, err := fixture.orchestratorWithMax(t, 3).Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var main *domain.ProxyGroup
	for i := range pool {
		if pool[i].ID == "agw-main" {
			main = &pool[i]
			break
		}
	}
	if main == nil || main.EgressSource != domain.EgressSourceMain || main.PublicPort != 8443 || main.ExitIP != "203.0.113.20" {
		t.Fatalf("main group=%#v pool=%#v", main, pool)
	}
}

func TestPoolAttachesPersistedProtocolStateToLiveEgress(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "node-one")
	group.Status = domain.ProxyGroupReady
	group.AimiliSlot = 0
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.PublicInboundID = 21
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 3, UpdatedAt: fixture.now()}
	fixture.aimili.candidates = []aimili.Candidate{{ID: "node-one", CountryCode: "JP", ProxyType: "datacenter", ProbeStatus: "available"}}

	pool, err := fixture.orchestratorWithMax(t, 3).Pool(context.Background())
	if err != nil || len(pool) != 1 {
		t.Fatalf("pool=%#v err=%v", pool, err)
	}
	if pool[0].ProtocolMode != domain.ProtocolVLESSXHTTPReality || pool[0].DesiredProtocolMode != domain.ProtocolVLESSXHTTPReality || pool[0].ProtocolState != domain.ProtocolReady {
		t.Fatalf("protocol state was not attached: %#v", pool[0])
	}
}

func TestPoolMarksLiveEgressWithoutProtocolStateAsRepairRequired(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "node-one")
	group.Status = domain.ProxyGroupReady
	group.AimiliSlot = 0
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.aimili.candidates = []aimili.Candidate{{ID: "node-one", CountryCode: "JP", ProxyType: "datacenter", ProbeStatus: "available"}}

	pool, err := fixture.orchestratorWithMax(t, 3).Pool(context.Background())
	if err != nil || len(pool) != 1 {
		t.Fatalf("pool=%#v err=%v", pool, err)
	}
	if pool[0].Status != domain.ProxyGroupRepairRequired || pool[0].ProtocolState != domain.ProtocolRepairRequired || pool[0].ProtocolLastErrorCode != "protocol_state_missing" {
		t.Fatalf("missing protocol state was hidden: %#v", pool[0])
	}
}

func TestEnableCompensatesInReverseOrderWhenVLESSValidationFails(t *testing.T) {
	fixture := newFixture()
	fixture.validator.vlessError = &validator.Error{Code: "protocol_failed"}
	_, err := fixture.orchestrator(t).Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter})
	if codeOf(err) != "protocol_failed" {
		t.Fatalf("error = %v", err)
	}
	wantTail := []string{"validate.vless", "xui.delete", "slot.delete"}
	if len(fixture.calls) < len(wantTail) || !equalStrings(fixture.calls[len(fixture.calls)-len(wantTail):], wantTail) {
		t.Fatalf("rollback calls = %#v", fixture.calls)
	}
	if len(fixture.store.groups) != 0 {
		t.Fatalf("rolled back group remains: %#v", fixture.store.groups)
	}
}

func TestEnableMarksRepairRequiredWhenCompensationFails(t *testing.T) {
	fixture := newFixture()
	fixture.validator.vlessError = &validator.Error{Code: "protocol_failed"}
	fixture.xui.deleteError = errors.New("delete failed")
	_, err := fixture.orchestrator(t).Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter})
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	group := fixture.store.groups["agw-jp-dc"]
	if group.Status != domain.ProxyGroupRepairRequired || group.LastErrorCode != "protocol_failed" {
		t.Fatalf("repair state = %#v", group)
	}
}

func TestCheckNeverRotatesAndRotateKeepsEntryStable(t *testing.T) {
	fixture := newFixture()
	orchestrator := fixture.orchestrator(t)
	created, err := orchestrator.Enable(context.Background(), EnableRequest{CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter})
	if err != nil {
		t.Fatal(err)
	}
	fixture.calls = nil
	if _, err := orchestrator.Check(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if contains(fixture.calls, "slot.rotate") {
		t.Fatal("check rotated the exit slot")
	}
	fixture.calls = nil
	fixture.aimili.rotatedExitIP = "203.0.113.8"
	rotated, err := orchestrator.Rotate(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ExitIP != "203.0.113.8" || rotated.PublicPort != created.PublicPort || rotated.MixedPort != created.MixedPort || rotated.ResourceName != created.ResourceName {
		t.Fatalf("rotate changed stable entry: before=%#v after=%#v", created, rotated)
	}
}

func TestCheckSynchronizesRuntimeCandidateIdentity(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "stale-node")
	group.Status = domain.ProxyGroupReady
	group.AimiliSlot = 2
	group.PublicInboundID = 21
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.ExitIP = "203.0.113.7"
	group.RealityPublicKey = "pk"
	group.RealityShortID = "sid"
	group.RealityServerName = "proxy.example.test"
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.aimili.createdSlots = map[int]aimili.Slot{2: {
		Number: 2, NodeID: "runtime-node", Country: "KR", CountryName: "韩国", ProxyType: "residential",
		CandidateIP: "198.51.100.8", ExitIP: "203.0.113.8", Port: 17930, Status: "up", EgressOK: true, LatencyMS: 44,
	}}

	checked, err := fixture.orchestratorWithMax(t, 3).Check(context.Background(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checked.CandidateID != "runtime-node" || checked.CandidateIP != "198.51.100.8" || checked.CountryCode != "KR" || checked.ProxyType != domain.ProxyTypeResidential || checked.ExitIP != "203.0.113.8" {
		t.Fatalf("runtime slot was not synchronized: %#v", checked)
	}
}

func TestCheckValidatesTheCurrentXHTTPProfileInsteadOfAssumingTCP(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "node-one")
	group.Status = domain.ProxyGroupReady
	group.AimiliSlot = 2
	group.PublicInboundID = 21
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.ExitIP = "203.0.113.7"
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.aimili.createdSlots = map[int]aimili.Slot{2: {Number: 2, NodeID: "node-one", Country: "JP", ProxyType: "datacenter", ExitIP: "203.0.113.7", Port: 17930, Status: "up", EgressOK: true}}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: 21, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "test-client", PublicKey: "test-public", ShortID: "test-short", ServerName: "proxy.example.test", XHTTPPath: "/test-path"}}

	checked, err := fixture.orchestratorWithMax(t, 3).Check(context.Background(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != domain.ProxyGroupReady || fixture.validator.vlessCalls != 0 || len(fixture.validator.publicTargets) != 1 || fixture.validator.publicTargets[0].Mode != domain.ProtocolVLESSXHTTPReality || fixture.validator.publicTargets[0].XHTTPPath != "/test-path" {
		t.Fatalf("checked=%#v vlessCalls=%d publicTargets=%#v", checked, fixture.validator.vlessCalls, fixture.validator.publicTargets)
	}
}

type fakeStore struct {
	mu                   sync.Mutex
	groups               map[string]domain.ProxyGroup
	credentials          map[string][]byte
	cidrs                []netip.Prefix
	policy               store.MixedSourcePolicy
	enforceUniqueSlots   bool
	mainEgress           store.MainEgress
	subscription         store.GatewaySubscription
	aggregate            store.AggregateConfig
	protocolModes        map[string]domain.EgressProtocolMode
	protocolUpdates      int
	protocolCreateError  error
	protocolUpdateErrors map[int]error
}

func (s *fakeStore) CreateEgressProtocolMode(_ context.Context, value domain.EgressProtocolMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.protocolCreateError != nil {
		return s.protocolCreateError
	}
	if _, exists := s.protocolModes[value.EgressID]; exists {
		return errors.New("protocol mode already exists")
	}
	s.protocolModes[value.EgressID] = value
	return nil
}

func (s *fakeStore) GetEgressProtocolMode(_ context.Context, egressID string) (domain.EgressProtocolMode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.protocolModes[egressID]
	if !ok {
		return domain.EgressProtocolMode{}, store.ErrEgressProtocolNotFound
	}
	return value, nil
}

func (s *fakeStore) ListEgressProtocolModes(context.Context) ([]domain.EgressProtocolMode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]domain.EgressProtocolMode, 0, len(s.protocolModes))
	for _, value := range s.protocolModes {
		result = append(result, value)
	}
	return result, nil
}

func (s *fakeStore) UpdateEgressProtocolMode(_ context.Context, value domain.EgressProtocolMode, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	nextUpdate := s.protocolUpdates + 1
	if err := s.protocolUpdateErrors[nextUpdate]; err != nil {
		s.protocolUpdates++
		return err
	}
	current, ok := s.protocolModes[value.EgressID]
	if !ok || current.Version != expectedVersion {
		return store.ErrEgressProtocolChanged
	}
	value.Version = expectedVersion + 1
	s.protocolModes[value.EgressID] = value
	s.protocolUpdates++
	return nil
}

func (s *fakeStore) CreateProxyGroup(_ context.Context, group domain.ProxyGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.groups[group.ID]; exists {
		return store.ErrProxyGroupExists
	}
	if s.enforceUniqueSlots {
		for _, current := range s.groups {
			if current.AimiliSlot == group.AimiliSlot {
				return store.ErrProxyGroupExists
			}
		}
	}
	s.groups[group.ID] = group
	return nil
}
func (s *fakeStore) GetProxyGroup(_ context.Context, id string) (domain.ProxyGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.groups[id]
	if !exists {
		return domain.ProxyGroup{}, store.ErrProxyGroupNotFound
	}
	return group, nil
}
func (s *fakeStore) ListProxyGroups(context.Context) ([]domain.ProxyGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]domain.ProxyGroup, 0, len(s.groups))
	for _, group := range s.groups {
		result = append(result, group)
	}
	return result, nil
}
func (s *fakeStore) UpdateProxyGroup(_ context.Context, group domain.ProxyGroup, expected int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.groups[group.ID]
	if !exists || current.Version != expected {
		return store.ErrProxyGroupChanged
	}
	group.Version = expected + 1
	s.groups[group.ID] = group
	return nil
}
func (s *fakeStore) DeleteProxyGroup(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.groups, id)
	delete(s.protocolModes, id)
	return nil
}
func (s *fakeStore) GetCredential(_ context.Context, purpose string, _ []byte) ([]byte, error) {
	value, exists := s.credentials[purpose]
	if !exists {
		return nil, store.ErrCredentialNotFound
	}
	return append([]byte(nil), value...), nil
}
func (s *fakeStore) ListMixedCIDRs(context.Context) ([]netip.Prefix, error) {
	return append([]netip.Prefix(nil), s.cidrs...), nil
}
func (s *fakeStore) ReplaceMixedCIDRs(_ context.Context, values []netip.Prefix) error {
	s.cidrs = append([]netip.Prefix(nil), values...)
	return nil
}
func (s *fakeStore) GetMixedSourcePolicy(context.Context) (store.MixedSourcePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.policy
	result.CIDRs = append([]netip.Prefix(nil), result.CIDRs...)
	return result, nil
}
func (s *fakeStore) ReplaceMixedSourcePolicy(_ context.Context, policy store.MixedSourcePolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy.CIDRs = append([]netip.Prefix(nil), policy.CIDRs...)
	s.policy = policy
	s.cidrs = append([]netip.Prefix(nil), policy.CIDRs...)
	return nil
}
func (s *fakeStore) SaveMainEgress(_ context.Context, value store.MainEgress) error {
	s.mainEgress = value
	return nil
}
func (s *fakeStore) GetMainEgress(context.Context) (store.MainEgress, error) {
	if s.mainEgress.ResourceName == "" {
		return store.MainEgress{}, store.ErrProxyGroupNotFound
	}
	return s.mainEgress, nil
}
func (s *fakeStore) SaveGatewaySubscription(_ context.Context, value store.GatewaySubscription) error {
	s.subscription = value
	return nil
}
func (s *fakeStore) GetAggregateConfig(context.Context) (store.AggregateConfig, error) {
	return s.aggregate, nil
}
func (s *fakeStore) SaveAggregateConfig(_ context.Context, value store.AggregateConfig) error {
	s.aggregate = value
	return nil
}

type fakeAimili struct {
	calls                     *[]string
	rotatedExitIP             string
	rotatedExitIPs            []string
	rotateCalls               int
	unreadyChecks             int
	candidates                []aimili.Candidate
	slotsByCandidate          map[string]aimili.Slot
	createdSlots              map[int]aimili.Slot
	createErrors              map[string]error
	mainStatus                aimili.MainStatus
	assignedSlot              aimili.Slot
	assignedSlots             []aimili.Slot
	assignErrors              []error
	assignCalls               int
	checkResults              []aimili.SlotCheck
	assignRequests            []aimili.AssignSlotRequest
	stagedMainStatus          aimili.MainStatus
	mainAssignment            aimili.MainAssignmentStatus
	mainRollbackError         error
	mainCommitErrors          []error
	mainCommitCalls           int
	repairCommitCalls         int
	repairReplaceRequests     []aimili.MainRepairRequest
	repairCommitErrors        []error
	repairReplaceErrors       []error
	rotateEntered             chan struct{}
	mutationLeaseExpires      float64
	mutationLeaseAcquireError error
	mutationLeaseRenewError   error
	mutationLeaseRenewed      chan struct{}
	mutationLeaseRenewErrors  chan error
}

func (a *fakeAimili) MainAssignment(context.Context) (aimili.MainAssignmentStatus, error) {
	if a.mainAssignment.State == "" {
		return aimili.MainAssignmentStatus{State: "idle"}, nil
	}
	return a.mainAssignment, nil
}

func (a *fakeAimili) StageMainAssignment(_ context.Context, request aimili.MainAssignmentRequest) (aimili.MainAssignmentStatus, error) {
	*a.calls = append(*a.calls, "main.stage")
	a.mainStatus = a.stagedMainStatus
	return aimili.MainAssignmentStatus{OperationID: "operation-safe-1", State: "pending_commit", OldCandidateID: request.ExpectedCurrentCandidateID, NewCandidateID: request.CandidateID, Country: request.Country, ProxyType: request.ProxyType, Port: 7928, DNSVerified: true, ExitVerified: true, Available: true}, nil
}
func (a *fakeAimili) CommitMainAssignment(context.Context, string) (aimili.MainAssignmentStatus, error) {
	*a.calls = append(*a.calls, "main.commit")
	index := a.mainCommitCalls
	a.mainCommitCalls++
	a.mainAssignment.State = "committed"
	if index < len(a.mainCommitErrors) && a.mainCommitErrors[index] != nil {
		return aimili.MainAssignmentStatus{}, a.mainCommitErrors[index]
	}
	return aimili.MainAssignmentStatus{OperationID: "operation-safe-1", State: "committed"}, nil
}
func (a *fakeAimili) RollbackMainAssignment(context.Context, string) (aimili.MainAssignmentStatus, error) {
	*a.calls = append(*a.calls, "main.rollback")
	if a.mainRollbackError != nil {
		return aimili.MainAssignmentStatus{}, a.mainRollbackError
	}
	a.mainStatus = aimili.MainStatus{CandidateID: "old-main", Country: "US", CountryName: "United States", ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928, EgressOK: true, Active: true}
	return aimili.MainAssignmentStatus{OperationID: "operation-safe-1", State: "rolled_back"}, nil
}

func (a *fakeAimili) RepairCommitMainAssignment(context.Context, string) (aimili.MainAssignmentStatus, error) {
	*a.calls = append(*a.calls, "main.repair-commit")
	index := a.repairCommitCalls
	a.repairCommitCalls++
	a.mainStatus = a.stagedMainStatus
	a.mainAssignment.State = "pending_gateway_validation"
	a.mainAssignment.DNSVerified = true
	a.mainAssignment.ExitVerified = true
	a.mainAssignment.Available = true
	if index < len(a.repairCommitErrors) && a.repairCommitErrors[index] != nil {
		return aimili.MainAssignmentStatus{}, a.repairCommitErrors[index]
	}
	return a.mainAssignment, nil
}

func (a *fakeAimili) RepairReplaceMainAssignment(_ context.Context, _ string, request aimili.MainRepairRequest) (aimili.MainAssignmentStatus, error) {
	*a.calls = append(*a.calls, "main.repair-replace")
	a.repairReplaceRequests = append(a.repairReplaceRequests, request)
	a.mainStatus = a.stagedMainStatus
	a.mainAssignment.State = "pending_gateway_validation"
	a.mainAssignment.NewCandidateID = request.CandidateID
	a.mainAssignment.Country = request.Country
	a.mainAssignment.ProxyType = request.ProxyType
	a.mainAssignment.DNSVerified = true
	a.mainAssignment.ExitVerified = true
	a.mainAssignment.Available = true
	index := len(a.repairReplaceRequests) - 1
	if index < len(a.repairReplaceErrors) && a.repairReplaceErrors[index] != nil {
		return aimili.MainAssignmentStatus{}, a.repairReplaceErrors[index]
	}
	return a.mainAssignment, nil
}

func (a *fakeAimili) MainStatus(context.Context) (aimili.MainStatus, error) { return a.mainStatus, nil }
func (a *fakeAimili) AcquireMutationLease(_ context.Context, _ string) (aimili.MutationLease, error) {
	*a.calls = append(*a.calls, "main.lease.acquire")
	if a.mutationLeaseAcquireError != nil {
		return aimili.MutationLease{}, a.mutationLeaseAcquireError
	}
	expires := a.mutationLeaseExpires
	if expires == 0 {
		expires = float64(time.Now().Add(time.Minute).Unix())
	}
	return aimili.MutationLease{State: "active", LeaseID: "opaque-lease-safe-1", ExpiresAt: expires}, nil
}
func (a *fakeAimili) RenewMutationLease(_ context.Context, leaseID string) (aimili.MutationLease, error) {
	if a.mutationLeaseRenewed != nil {
		select {
		case a.mutationLeaseRenewed <- struct{}{}:
		default:
		}
	}
	if a.mutationLeaseRenewError != nil {
		return aimili.MutationLease{}, a.mutationLeaseRenewError
	}
	if a.mutationLeaseRenewErrors != nil {
		select {
		case err := <-a.mutationLeaseRenewErrors:
			if err != nil {
				return aimili.MutationLease{}, err
			}
		default:
		}
	}
	return aimili.MutationLease{State: "active", LeaseID: leaseID, ExpiresAt: float64(time.Now().Add(time.Minute).Unix())}, nil
}
func (a *fakeAimili) ReleaseMutationLease(context.Context, string) error {
	*a.calls = append(*a.calls, "main.lease.release")
	return nil
}
func (a *fakeAimili) AssignSlotNode(_ context.Context, number int, request aimili.AssignSlotRequest) (aimili.Slot, error) {
	a.assignRequests = append(a.assignRequests, request)
	index := a.assignCalls
	a.assignCalls++
	if index < len(a.assignErrors) && a.assignErrors[index] != nil {
		return aimili.Slot{}, a.assignErrors[index]
	}
	result := a.assignedSlot
	if index < len(a.assignedSlots) {
		result = a.assignedSlots[index]
	}
	result.Number = number
	if a.createdSlots == nil {
		a.createdSlots = make(map[int]aimili.Slot)
	}
	a.createdSlots[number] = result
	return result, nil
}

func (a *fakeAimili) Candidates(context.Context) ([]aimili.Candidate, error) {
	if a.candidates != nil {
		return append([]aimili.Candidate(nil), a.candidates...), nil
	}
	return []aimili.Candidate{{CountryCode: "JP", CountryName: "日本", ProxyType: "datacenter", ProbeStatus: "available"}}, nil
}
func (a *fakeAimili) CreateSlot(_ context.Context, request aimili.CreateSlotRequest) (aimili.Slot, error) {
	*a.calls = append(*a.calls, "slot.create")
	if err := a.createErrors[request.CandidateID]; err != nil {
		return aimili.Slot{}, err
	}
	if slot, ok := a.slotsByCandidate[request.CandidateID]; ok {
		if a.createdSlots == nil {
			a.createdSlots = make(map[int]aimili.Slot)
		}
		a.createdSlots[slot.Number] = slot
		return slot, nil
	}
	return a.slot("203.0.113.7"), nil
}
func (a *fakeAimili) ListSlots(context.Context) ([]aimili.Slot, error) {
	result := make([]aimili.Slot, 0, len(a.createdSlots))
	for _, slot := range a.createdSlots {
		result = append(result, slot)
	}
	return result, nil
}
func (a *fakeAimili) CheckSlot(_ context.Context, number int) (aimili.SlotCheck, error) {
	*a.calls = append(*a.calls, "slot.check")
	if len(a.checkResults) > 0 {
		result := a.checkResults[0]
		a.checkResults = a.checkResults[1:]
		result.Number = number
		a.createdSlots[number] = result
		return result, nil
	}
	if a.unreadyChecks > 0 {
		a.unreadyChecks--
		slot := a.slot("")
		slot.EgressOK = false
		slot.Status = "starting"
		return slot, nil
	}
	if slot, ok := a.createdSlots[number]; ok {
		return slot, nil
	}
	ip := a.rotatedExitIP
	if ip == "" {
		ip = "203.0.113.7"
	}
	return a.slot(ip), nil
}
func (a *fakeAimili) RotateSlot(_ context.Context, number int) (aimili.Slot, error) {
	*a.calls = append(*a.calls, "slot.rotate")
	if a.rotateEntered != nil {
		select {
		case <-a.rotateEntered:
		default:
			close(a.rotateEntered)
		}
	}
	if a.rotateCalls < len(a.rotatedExitIPs) {
		a.rotatedExitIP = a.rotatedExitIPs[a.rotateCalls]
		a.rotateCalls++
		rotated := a.slot(a.rotatedExitIP)
		rotated.Number = number
		if a.createdSlots != nil {
			a.createdSlots[number] = rotated
		}
		return rotated, nil
	}
	if a.rotatedExitIP == "" {
		a.rotatedExitIP = "203.0.113.8"
	}
	rotated := a.slot(a.rotatedExitIP)
	rotated.Number = number
	if a.createdSlots != nil {
		a.createdSlots[number] = rotated
	}
	return rotated, nil
}
func (a *fakeAimili) DeleteSlot(context.Context, int) error {
	*a.calls = append(*a.calls, "slot.delete")
	return nil
}
func (a *fakeAimili) slot(ip string) aimili.Slot {
	return aimili.Slot{Number: 0, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17930, Status: "up", ExitIP: ip, EgressOK: true}
}

type fakeXUI struct {
	calls                  *[]string
	deleteError            error
	desired                xui.DesiredGroup
	updated                []xui.DesiredGroup
	updateNames            []string
	updateErrors           map[int]error
	returnedPublicKey      string
	returnedShortID        string
	returnedServerName     string
	returnedVLESSInboundID int64
	returnedMixedInboundID int64
	returnedResourceName   string
	deletedAggregate       xui.ManagedAggregate
	deleteAggregateError   error
	snapshot               xui.Snapshot
	subscriptionDesired    xui.SubscriptionDesired
	subscriptionProfiles   []xui.PublicProfile
	profileSequences       [][]xui.PublicProfile
	ensureLegacyMainCalls  int
}

func (x *fakeXUI) Snapshot(context.Context) (xui.Snapshot, error) { return x.snapshot, nil }
func (x *fakeXUI) EnsureSubscriptionClient(_ context.Context, desired xui.SubscriptionDesired) (xui.Subscription, error) {
	x.subscriptionDesired = desired
	profiles := append([]xui.PublicProfile(nil), x.subscriptionProfiles...)
	if len(x.profileSequences) > 0 {
		profiles = append([]xui.PublicProfile(nil), x.profileSequences[0]...)
		x.profileSequences = x.profileSequences[1:]
	}
	if len(profiles) == 0 {
		for _, id := range desired.InboundIDs {
			profiles = append(profiles, xui.PublicProfile{InboundID: id, Mode: domain.ProtocolVLESSTCPRealityVision, ClientID: desired.ClientUUID, PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test"})
		}
	}
	return xui.Subscription{ResourceName: "aimili-gateway-subscription", ClientID: 42, ClientEmail: desired.ClientEmail, ClientUUID: desired.ClientUUID, SubscriptionID: "opaque", InboundIDs: append([]int64(nil), desired.InboundIDs...), SubscriptionPath: "/sub-test/", PublicProfiles: profiles}, nil
}
func (x *fakeXUI) SubscriptionURL(_ context.Context, subscription xui.Subscription) (string, error) {
	return subscription.SubscriptionPath + subscription.SubscriptionID, nil
}

func (x *fakeXUI) DeleteManagedAggregate(_ context.Context, managed xui.ManagedAggregate) error {
	x.deletedAggregate = managed
	return x.deleteAggregateError
}

func (x *fakeXUI) EnsureLegacyMain(_ context.Context, desired xui.LegacyMainDesired) (xui.LegacyMain, error) {
	x.ensureLegacyMainCalls++
	return xui.LegacyMain{VLESSInboundID: 1, MixedInboundID: 98, VLESSPort: desired.VLESSPort, MixedPort: desired.MixedPort, ClientID: "legacy-client", PublicKey: "legacy-public", ShortID: "legacy-short", ServerName: "www.microsoft.com", OutboundTag: "aimili-socks"}, nil
}

func (x *fakeXUI) EnsureManagedGroup(_ context.Context, desired xui.DesiredGroup) (xui.ManagedGroup, error) {
	*x.calls = append(*x.calls, "xui.ensure")
	x.desired = desired
	return xui.ManagedGroup{ResourceName: "agw-jp-dc", VLESSInboundID: 11, MixedInboundID: 12, VLESSInboundTag: "agw-jp-dc-vless", MixedInboundTag: "agw-jp-dc-mixed", OutboundTag: "agw-jp-dc-socks", Fingerprint: "fp", PublicKey: "pk", ShortID: "sid", ServerName: desired.RealityServerName}, nil
}
func (x *fakeXUI) DeleteManagedGroup(context.Context, xui.ManagedGroup) error {
	*x.calls = append(*x.calls, "xui.delete")
	return x.deleteError
}
func (x *fakeXUI) UpdateManagedGroup(_ context.Context, desired xui.DesiredGroup, managed xui.ManagedGroup) (xui.ManagedGroup, error) {
	x.updateNames = append(x.updateNames, desired.ResourceName)
	x.updated = append(x.updated, desired)
	if err := x.updateErrors[len(x.updateNames)]; err != nil {
		return xui.ManagedGroup{}, err
	}
	managed.Fingerprint = "new-" + desired.ResourceName
	if x.returnedPublicKey != "" {
		managed.PublicKey = x.returnedPublicKey
	}
	if x.returnedShortID != "" {
		managed.ShortID = x.returnedShortID
	}
	if x.returnedServerName != "" {
		managed.ServerName = x.returnedServerName
	}
	if x.returnedVLESSInboundID != 0 {
		managed.VLESSInboundID = x.returnedVLESSInboundID
	}
	if x.returnedMixedInboundID != 0 {
		managed.MixedInboundID = x.returnedMixedInboundID
	}
	if x.returnedResourceName != "" {
		managed.ResourceName = x.returnedResourceName
		managed.VLESSInboundTag = x.returnedResourceName + "-vless"
		managed.MixedInboundTag = x.returnedResourceName + "-mixed"
		managed.OutboundTag = x.returnedResourceName + "-socks"
	}
	return managed, nil
}

type fakeValidator struct {
	calls            *[]string
	vlessError       error
	vlessErrors      []error
	vlessCalls       int
	socksCalls       int
	socksErrors      []error
	socksExpectedIPs []string
	socksLatency     time.Duration
	vlessLatency     time.Duration
	publicErrors     []error
	publicTargets    []validator.PublicTarget
}

func (v *fakeValidator) ValidateSOCKS5H(_ context.Context, target validator.SOCKSTarget) (validator.Result, error) {
	*v.calls = append(*v.calls, "validate.socks")
	v.socksCalls++
	v.socksExpectedIPs = append(v.socksExpectedIPs, target.ExpectedExitIP)
	if v.socksCalls <= len(v.socksErrors) && v.socksErrors[v.socksCalls-1] != nil {
		return validator.Result{}, v.socksErrors[v.socksCalls-1]
	}
	return validator.Result{ExitIP: target.ExpectedExitIP, DNSVerified: true, Latency: v.socksLatency}, nil
}
func (v *fakeValidator) ValidateVLESS(_ context.Context, target validator.VLESSTarget) (validator.Result, error) {
	*v.calls = append(*v.calls, "validate.vless")
	v.vlessCalls++
	if v.vlessCalls <= len(v.vlessErrors) && v.vlessErrors[v.vlessCalls-1] != nil {
		return validator.Result{}, v.vlessErrors[v.vlessCalls-1]
	}
	if v.vlessError != nil {
		return validator.Result{}, v.vlessError
	}
	return validator.Result{ExitIP: target.ExpectedExitIP, DNSVerified: true, Latency: v.vlessLatency}, nil
}
func (v *fakeValidator) ValidatePublic(_ context.Context, target validator.PublicTarget) (validator.Result, error) {
	*v.calls = append(*v.calls, "validate.public")
	v.publicTargets = append(v.publicTargets, target)
	index := len(v.publicTargets) - 1
	if index < len(v.publicErrors) && v.publicErrors[index] != nil {
		return validator.Result{}, v.publicErrors[index]
	}
	return validator.Result{ExitIP: target.ExpectedExitIP, DNSVerified: true, Latency: v.vlessLatency}, nil
}

type fixture struct {
	calls     []string
	store     *fakeStore
	aimili    *fakeAimili
	xui       *fakeXUI
	validator *fakeValidator
}

func newFixture() *fixture {
	f := &fixture{}
	f.store = &fakeStore{groups: map[string]domain.ProxyGroup{}, protocolModes: map[string]domain.EgressProtocolMode{}, credentials: map[string][]byte{"vless-client-id": []byte("client-id"), "mixed-username": []byte("proxy-user"), "mixed-password": []byte("proxy-password")}, cidrs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}}
	f.store.policy = store.MixedSourcePolicy{Enabled: true, CIDRs: append([]netip.Prefix(nil), f.store.cidrs...), ApplyStatus: store.MixedPolicyApplied, UpdatedAt: f.now()}
	f.aimili = &fakeAimili{calls: &f.calls}
	f.xui = &fakeXUI{calls: &f.calls}
	f.validator = &fakeValidator{calls: &f.calls}
	return f
}
func (f *fixture) now() time.Time { return time.Unix(1700000000, 0).UTC() }
func (f *fixture) orchestrator(t *testing.T) *Orchestrator {
	return f.orchestratorWithMax(t, 1)
}
func (f *fixture) orchestratorWithMax(t *testing.T, max int) *Orchestrator {
	t.Helper()
	value, err := New(Config{MaxGroups: max, VLESSPortStart: 20000, VLESSPortEnd: 20009, MixedPortStart: 30000, MixedPortEnd: 30009, PublicHost: "proxy.example.test", XrayPath: "/xray", ProbeHost: "ip.example.test", ReadyTimeout: time.Second, PollInterval: time.Millisecond, Now: func() time.Time { return time.Unix(1700000000, 0).UTC() }}, f.store, f.aimili, f.xui, f.validator, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func codeOf(err error) string {
	var operationError *Error
	if errors.As(err, &operationError) {
		return operationError.Code
	}
	return ""
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
