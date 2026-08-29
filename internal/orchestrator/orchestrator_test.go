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
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.ExitIP = "203.0.113.7"
	group.RealityPublicKey = "pk"
	group.RealityShortID = "sid"
	group.RealityServerName = "proxy.example.test"
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
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

type fakeStore struct {
	mu                 sync.Mutex
	groups             map[string]domain.ProxyGroup
	credentials        map[string][]byte
	cidrs              []netip.Prefix
	policy             store.MixedSourcePolicy
	enforceUniqueSlots bool
	mainEgress         store.MainEgress
	subscription       store.GatewaySubscription
	aggregate          store.AggregateConfig
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
	calls            *[]string
	rotatedExitIP    string
	rotatedExitIPs   []string
	rotateCalls      int
	unreadyChecks    int
	candidates       []aimili.Candidate
	slotsByCandidate map[string]aimili.Slot
	createdSlots     map[int]aimili.Slot
	createErrors     map[string]error
	mainStatus       aimili.MainStatus
	assignedSlot     aimili.Slot
	assignedSlots    []aimili.Slot
	assignErrors     []error
	assignCalls      int
	checkResults     []aimili.SlotCheck
	assignRequests   []aimili.AssignSlotRequest
}

func (a *fakeAimili) MainStatus(context.Context) (aimili.MainStatus, error) { return a.mainStatus, nil }
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
}

func (x *fakeXUI) Snapshot(context.Context) (xui.Snapshot, error) { return x.snapshot, nil }
func (x *fakeXUI) EnsureSubscriptionClient(_ context.Context, desired xui.SubscriptionDesired) (xui.Subscription, error) {
	x.subscriptionDesired = desired
	return xui.Subscription{ResourceName: "aimili-gateway-subscription", ClientID: 42, ClientEmail: desired.ClientEmail, ClientUUID: desired.ClientUUID, SubscriptionID: "opaque", InboundIDs: append([]int64(nil), desired.InboundIDs...), SubscriptionPath: "/sub-test/"}, nil
}
func (x *fakeXUI) SubscriptionURL(_ context.Context, subscription xui.Subscription) (string, error) {
	return subscription.SubscriptionPath + subscription.SubscriptionID, nil
}

func (x *fakeXUI) DeleteManagedAggregate(_ context.Context, managed xui.ManagedAggregate) error {
	x.deletedAggregate = managed
	return x.deleteAggregateError
}

func (x *fakeXUI) EnsureLegacyMain(_ context.Context, desired xui.LegacyMainDesired) (xui.LegacyMain, error) {
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

type fixture struct {
	calls     []string
	store     *fakeStore
	aimili    *fakeAimili
	xui       *fakeXUI
	validator *fakeValidator
}

func newFixture() *fixture {
	f := &fixture{}
	f.store = &fakeStore{groups: map[string]domain.ProxyGroup{}, credentials: map[string][]byte{"vless-client-id": []byte("client-id"), "mixed-username": []byte("proxy-user"), "mixed-password": []byte("proxy-password")}, cidrs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}}
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
