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
		group.VLESSPort != 20000 || group.MixedPort != 30000 || group.VLESSInboundID == 0 || group.MixedInboundID == 0 {
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

func TestEnableRotatesANewSlotUntilItsExitIsUnique(t *testing.T) {
	fixture := newFixture()
	existing, _ := domain.NewProxyGroupIdentity("US", domain.ProxyTypeDatacenter, "existing")
	existing.Status = domain.ProxyGroupReady
	existing.ExitIP = "203.0.113.7"
	existing.VLESSPort = 20000
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
	existing.VLESSPort = 20000
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
	if rotated.ExitIP != "203.0.113.8" || rotated.VLESSPort != created.VLESSPort || rotated.MixedPort != created.MixedPort || rotated.ResourceName != created.ResourceName {
		t.Fatalf("rotate changed stable entry: before=%#v after=%#v", created, rotated)
	}
}

type fakeStore struct {
	mu          sync.Mutex
	groups      map[string]domain.ProxyGroup
	credentials map[string][]byte
	cidrs       []netip.Prefix
	policy      store.MixedSourcePolicy
}

func (s *fakeStore) CreateProxyGroup(_ context.Context, group domain.ProxyGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.groups[group.ID]; exists {
		return store.ErrProxyGroupExists
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
	socksCalls       int
	socksErrors      []error
	socksExpectedIPs []string
}

func (v *fakeValidator) ValidateSOCKS5H(_ context.Context, target validator.SOCKSTarget) (validator.Result, error) {
	*v.calls = append(*v.calls, "validate.socks")
	v.socksCalls++
	v.socksExpectedIPs = append(v.socksExpectedIPs, target.ExpectedExitIP)
	if v.socksCalls <= len(v.socksErrors) && v.socksErrors[v.socksCalls-1] != nil {
		return validator.Result{}, v.socksErrors[v.socksCalls-1]
	}
	return validator.Result{ExitIP: "203.0.113.7", DNSVerified: true}, nil
}
func (v *fakeValidator) ValidateVLESS(_ context.Context, target validator.VLESSTarget) (validator.Result, error) {
	*v.calls = append(*v.calls, "validate.vless")
	if v.vlessError != nil {
		return validator.Result{}, v.vlessError
	}
	return validator.Result{ExitIP: target.ExpectedExitIP, DNSVerified: true}, nil
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
