package maintenance

import (
	"context"
	"encoding/json"
	"strings"
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
		{ID: "agw-jp-res-a", ResourceName: "agw-jp-res-a", Status: domain.ProxyGroupReady, VLESSInboundID: 11, MixedInboundID: 12, ConfigFingerprint: "fingerprint-one", LastCheckedAt: now},
	}
	xuiSource := &fakeXUISource{snapshot: xui.Snapshot{
		Inbounds:        []xui.Inbound{{ID: 11, Tag: "agw-jp-res-a-vless", Protocol: "vless"}, {ID: 12, Tag: "agw-jp-res-a-mixed", Protocol: "mixed"}},
		Outbounds:       []xui.Outbound{{Tag: "agw-jp-res-a-socks", Protocol: "socks"}, {Tag: "user-outbound", Protocol: "freedom"}},
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
	if xuiSummary.ManagedVLESSCount != 1 || xuiSummary.ManagedMixedCount != 1 || xuiSummary.ManagedOutboundCount != 1 || !xuiSummary.OwnershipMatches {
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
	groups := []domain.ProxyGroup{{ID: "agw-jp-dc", ResourceName: "agw-jp-dc", Status: domain.ProxyGroupReady, AimiliSlot: 7, VLESSInboundID: 11, MixedInboundID: 12}}
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

type fakeAimiliSource struct {
	candidates   []aimili.Candidate
	slots        []aimili.Slot
	checkedSlots []int
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
