package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestServiceReturnsOnlyApprovedMaintenanceSummaries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	aimiliSource := &fakeAimiliSource{
		candidates: []aimili.Candidate{
			{ID: "secret-candidate-one", CountryCode: "JP", ProxyType: "residential", ProbeStatus: "available", LastProbeAt: float64(now.Unix())},
			{ID: "secret-candidate-two", CountryCode: "US", ProxyType: "datacenter", ProbeStatus: "available", LastProbeAt: float64(now.Add(time.Minute).Unix())},
			{ID: "ignored", CountryCode: "DE", ProxyType: "datacenter", ProbeStatus: "failed"},
		},
		slots: []aimili.Slot{{Number: 1, EgressOK: true}},
	}
	groups := []domain.ProxyGroup{
		{ID: "agw-jp-res-a", ResourceName: "agw-jp-res-a", Status: domain.ProxyGroupReady, PublicInboundID: 11, MixedInboundID: 12, ConfigFingerprint: "fingerprint-one", LastCheckedAt: now},
	}
	xuiSource := &fakeXUISource{snapshot: xui.Snapshot{
		Inbounds:        []xui.Inbound{{ID: 1, Tag: "aimili-reality", Protocol: "vless"}, {ID: 2, Tag: "agw-main-mixed", Protocol: "mixed"}, {ID: 11, Tag: "agw-jp-res-a-vless", Protocol: "vless"}, {ID: 12, Tag: "agw-jp-res-a-mixed", Protocol: "mixed"}},
		Outbounds:       []xui.Outbound{{Tag: "aimili-socks", Protocol: "socks"}, {Tag: "agw-jp-res-a-socks", Protocol: "socks"}, {Tag: "user-outbound", Protocol: "freedom"}},
		OutboundTestURL: "https://must-not-escape.invalid/secret",
	}}
	groupSource := &fakeGroupSource{groups: groups}
	accounts := &fakeAccountStatus{state: store.AccountSyncState{Status: store.AccountSyncSynced, ErrorCode: "must-not-escape"}}
	service, err := New(Config{MaxOnline: 1}, aimiliSource, xuiSource, groupSource, accounts)
	if err != nil {
		t.Fatal(err)
	}

	summary, err := service.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.CandidateCount != 2 || summary.OnlineCount != 1 || summary.MaxOnline != 1 || summary.AccountSyncStatus != store.AccountSyncSynced {
		t.Fatalf("summary = %#v", summary)
	}
	aimiliSummary, err := service.AimiliVPN(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if aimiliSummary.CandidateCount != 2 || aimiliSummary.ResidentialCount != 1 || aimiliSummary.DatacenterCount != 1 || aimiliSummary.ManagedSlotCount != 1 || !aimiliSummary.LastRefreshedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("Aimili summary = %#v", aimiliSummary)
	}
	xuiSummary, err := service.XUI(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if xuiSummary.ManagedPublicCount != 2 || xuiSummary.ManagedVLESSCount != 2 || xuiSummary.ManagedMixedCount != 2 || xuiSummary.ManagedOutboundCount != 2 || !xuiSummary.OwnershipMatches {
		t.Fatalf("3x-ui summary = %#v", xuiSummary)
	}

	encoded, _ := json.Marshal([]any{summary, aimiliSummary, xuiSummary})
	for _, forbidden := range []string{"secret-candidate", "fingerprint-one", "must-not-escape", "OutboundTestURL"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("maintenance response leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestServiceChecksManagedSlotsAndRepairsOnlyManagedResources(t *testing.T) {
	groups := []domain.ProxyGroup{{ID: "agw-jp-dc", ResourceName: "agw-jp-dc", Status: domain.ProxyGroupReady, AimiliSlot: 7, PublicInboundID: 11, MixedInboundID: 12}}
	aimiliSource := &fakeAimiliSource{slots: []aimili.Slot{{Number: 7, EgressOK: true}}}
	groupSource := &fakeGroupSource{groups: groups}
	service, err := New(Config{MaxOnline: 1}, aimiliSource, &fakeXUISource{}, groupSource, &fakeAccountStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CheckAimiliVPN(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(aimiliSource.checkedSlots) != 1 || aimiliSource.checkedSlots[0] != 7 {
		t.Fatalf("checked slots = %#v", aimiliSource.checkedSlots)
	}
	if _, err := service.RepairXUI(context.Background()); err != nil {
		t.Fatal(err)
	}
	if groupSource.repairCalls != 1 {
		t.Fatalf("repair calls = %d", groupSource.repairCalls)
	}
}

func TestXUISummaryRecognizesMainAndMixedPublicProtocols(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	groups := []domain.ProxyGroup{
		{ID: "agw-a", ResourceName: "agw-a", PublicInboundID: 11, MixedInboundID: 12, ConfigFingerprint: "a", LastCheckedAt: now},
		{ID: "agw-b", ResourceName: "agw-b", PublicInboundID: 21, MixedInboundID: 22, ConfigFingerprint: "b", LastCheckedAt: now},
		{ID: "agw-c", ResourceName: "agw-c", PublicInboundID: 31, MixedInboundID: 32, ConfigFingerprint: "c", LastCheckedAt: now},
	}
	snapshot := xui.Snapshot{
		Inbounds: []xui.Inbound{
			{ID: 1, Tag: "aimili-reality", Protocol: "vless"}, {ID: 2, Tag: "agw-main-mixed", Protocol: "mixed"},
			{ID: 11, Tag: "agw-a-vless", Protocol: "vless"}, {ID: 12, Tag: "agw-a-mixed", Protocol: "mixed"},
			{ID: 21, Tag: "agw-b-vless", Protocol: "hysteria"}, {ID: 22, Tag: "agw-b-mixed", Protocol: "mixed"},
			{ID: 31, Tag: "agw-c-vless", Protocol: "vless"}, {ID: 32, Tag: "agw-c-mixed", Protocol: "mixed"},
		},
		Outbounds: []xui.Outbound{
			{Tag: "aimili-socks", Protocol: "socks"},
			{Tag: "agw-a-socks", Protocol: "socks"}, {Tag: "agw-b-socks", Protocol: "socks"}, {Tag: "agw-c-socks", Protocol: "socks"},
		},
	}
	service, err := New(Config{MaxOnline: 3}, &fakeAimiliSource{}, &fakeXUISource{snapshot: snapshot}, &fakeGroupSource{groups: groups}, &fakeAccountStatus{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.XUI(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.ManagedPublicCount != 4 || summary.ManagedVLESSCount != 3 || summary.ManagedMixedCount != 4 || summary.ManagedOutboundCount != 4 || !summary.OwnershipMatches {
		t.Fatalf("mixed protocol summary = %#v", summary)
	}
}

func TestServiceStartsCountryRefreshAndReconcilesAfterCompletion(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	aimiliSource := &fakeAimiliSource{
		countries:    []aimili.CandidateCountry{{Code: "JP", Name: "日本", CandidateCount: 8, ObservedAt: 1_700_000_000}},
		startRefresh: aimili.CountryRefresh{State: "running", Country: "JP", Phase: "fetching"},
		refreshes: []aimili.CountryRefresh{
			{State: "running", Country: "JP", Phase: "probing", TestedCount: 1},
			{State: "completed", Country: "JP", TestedCount: 5, ValidCount: 4},
		},
	}
	reconciled := make(chan struct{}, 1)
	service, err := New(Config{
		MaxOnline:       1,
		LifetimeContext: lifetime,
		PollInterval:    time.Millisecond,
		PollTimeout:     100 * time.Millisecond,
		Reconcile: func(context.Context) {
			reconciled <- struct{}{}
		},
	}, aimiliSource, &fakeXUISource{}, &fakeGroupSource{}, &fakeAccountStatus{})
	if err != nil {
		t.Fatal(err)
	}
	countries, err := service.CandidateCountries(context.Background())
	if err != nil || len(countries) != 1 || countries[0].Code != "JP" {
		t.Fatalf("countries = %#v, err = %v", countries, err)
	}
	started, err := service.StartAimiliVPNRefresh(context.Background(), "jp")
	if err != nil || started.State != "running" || started.Country != "JP" {
		t.Fatalf("started = %#v, err = %v", started, err)
	}
	select {
	case <-reconciled:
	case <-time.After(time.Second):
		t.Fatal("completed refresh did not trigger reconcile")
	}
	status, err := service.AimiliVPNRefresh(context.Background())
	if err != nil || status.State != "completed" || status.ValidCount != 4 {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestServicePreservesRefreshErrorCodesAndCancelsPollingWithGateway(t *testing.T) {
	aimiliSource := &fakeAimiliSource{startError: &aimili.AdapterError{Code: "maintenance_busy"}}
	service, err := New(Config{MaxOnline: 1}, aimiliSource, &fakeXUISource{}, &fakeGroupSource{}, &fakeAccountStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartAimiliVPNRefresh(context.Background(), "JP"); errorCode(err) != "maintenance_busy" {
		t.Fatalf("busy error = %v", err)
	}

	lifetime, cancel := context.WithCancel(context.Background())
	pollObserved := make(chan struct{}, 1)
	aimiliSource = &fakeAimiliSource{
		startRefresh: aimili.CountryRefresh{State: "running", Country: "JP"},
		refreshes:    []aimili.CountryRefresh{{State: "running", Country: "JP"}},
		pollObserved: pollObserved,
	}
	service, err = New(Config{
		MaxOnline:       1,
		LifetimeContext: lifetime,
		PollInterval:    time.Millisecond,
		PollTimeout:     time.Second,
		Reconcile: func(context.Context) {
			t.Error("canceled Gateway polling triggered reconcile")
		},
	}, aimiliSource, &fakeXUISource{}, &fakeGroupSource{}, &fakeAccountStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartAimiliVPNRefresh(context.Background(), "JP"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pollObserved:
	case <-time.After(time.Second):
		t.Fatal("refresh polling did not start")
	}
	cancel()
	time.Sleep(10 * time.Millisecond)
}

func errorCode(err error) string {
	var maintenanceError *Error
	if !errors.As(err, &maintenanceError) {
		return ""
	}
	return maintenanceError.Code
}

type fakeAimiliSource struct {
	candidates   []aimili.Candidate
	slots        []aimili.Slot
	checkedSlots []int
	countries    []aimili.CandidateCountry
	startRefresh aimili.CountryRefresh
	startError   error
	refreshes    []aimili.CountryRefresh
	pollObserved chan struct{}
	mu           sync.Mutex
}

func (fake *fakeAimiliSource) Candidates(context.Context) ([]aimili.Candidate, error) {
	return append([]aimili.Candidate(nil), fake.candidates...), nil
}
func (fake *fakeAimiliSource) ListSlots(context.Context) ([]aimili.Slot, error) {
	return append([]aimili.Slot(nil), fake.slots...), nil
}
func (fake *fakeAimiliSource) CheckSlot(_ context.Context, slot int) (aimili.SlotCheck, error) {
	fake.checkedSlots = append(fake.checkedSlots, slot)
	return aimili.SlotCheck{Number: slot, EgressOK: true}, nil
}
func (fake *fakeAimiliSource) CandidateCountries(context.Context) ([]aimili.CandidateCountry, error) {
	return append([]aimili.CandidateCountry(nil), fake.countries...), nil
}
func (fake *fakeAimiliSource) StartCountryRefresh(context.Context, string) (aimili.CountryRefresh, error) {
	return fake.startRefresh, fake.startError
}
func (fake *fakeAimiliSource) CountryRefresh(context.Context) (aimili.CountryRefresh, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.pollObserved != nil {
		select {
		case fake.pollObserved <- struct{}{}:
		default:
		}
	}
	if len(fake.refreshes) == 0 {
		return aimili.CountryRefresh{}, nil
	}
	result := fake.refreshes[0]
	if len(fake.refreshes) > 1 {
		fake.refreshes = fake.refreshes[1:]
	}
	return result, nil
}

type fakeXUISource struct{ snapshot xui.Snapshot }

func (fake *fakeXUISource) Snapshot(context.Context) (xui.Snapshot, error) { return fake.snapshot, nil }

type fakeGroupSource struct {
	groups      []domain.ProxyGroup
	repairCalls int
}

func (fake *fakeGroupSource) List(context.Context) ([]domain.ProxyGroup, error) {
	return append([]domain.ProxyGroup(nil), fake.groups...), nil
}
func (fake *fakeGroupSource) RepairManaged(context.Context) error { fake.repairCalls++; return nil }

type fakeAccountStatus struct{ state store.AccountSyncState }

func (fake *fakeAccountStatus) Status(context.Context) (store.AccountSyncState, error) {
	return fake.state, nil
}
