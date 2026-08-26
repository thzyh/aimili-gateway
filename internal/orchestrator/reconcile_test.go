package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/domain"
)

func TestReconcileCreatesEveryAvailableCandidateEvenWithinOneClassification(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "jp-one", CountryCode: "JP", CountryName: "日本", IP: "198.51.100.10", ProxyType: "datacenter", LatencyMS: 21, ProbeStatus: "available"},
		{ID: "jp-two", CountryCode: "JP", CountryName: "日本", IP: "198.51.100.11", ProxyType: "datacenter", LatencyMS: 35, ProbeStatus: "available"},
	}
	fixture.aimili.slotsByCandidate = map[string]aimili.Slot{
		"jp-one": {Number: 0, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17928, Status: "up", NodeID: "jp-one", CandidateIP: "198.51.100.10", ExitIP: "203.0.113.10", EgressOK: true},
		"jp-two": {Number: 1, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17929, Status: "up", NodeID: "jp-two", CandidateIP: "198.51.100.11", ExitIP: "203.0.113.11", EgressOK: true},
	}
	orchestrator := fixture.orchestratorWithMax(t, 4)

	result := orchestrator.Reconcile(context.Background())

	if result.Discovered != 2 || result.Ready != 2 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	groups, _ := fixture.store.ListProxyGroups(context.Background())
	if len(groups) != 2 || groups[0].CandidateID == groups[1].CandidateID {
		t.Fatalf("candidate groups were not independently created: %#v", groups)
	}
}

func TestReconcileAdoptsV1BLegacyGroupFromItsExistingSlot(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "jp-existing", CountryCode: "JP", CountryName: "日本", IP: "198.51.100.10", ProxyType: "datacenter", LatencyMS: 21, ProbeStatus: "available"},
	}
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		0: {Number: 0, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17928, Status: "up", NodeID: "jp-existing", CandidateIP: "198.51.100.10", ExitIP: "203.0.113.10", EgressOK: true},
	}
	legacy, err := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter)
	if err != nil {
		t.Fatal(err)
	}
	legacy.CountryName = "日本"
	legacy.Status = domain.ProxyGroupReady
	legacy.AimiliSlot = 0
	legacy.VLESSPort = 20000
	legacy.MixedPort = 30000
	legacy.ExitIP = "203.0.113.10"
	legacy.CreatedAt = time.Unix(1699999000, 0).UTC()
	legacy.UpdatedAt = legacy.CreatedAt
	fixture.store.groups[legacy.ID] = legacy

	result := fixture.orchestratorWithMax(t, 1).Reconcile(context.Background())
	adopted := fixture.store.groups[legacy.ID]

	if result.Discovered != 1 || result.Ready != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if adopted.CandidateID != "jp-existing" || adopted.CandidateIP != "198.51.100.10" || adopted.CandidateLatencyMS != 21 {
		t.Fatalf("legacy group was not adopted: %#v", adopted)
	}
	if contains(fixture.calls, "slot.create") || contains(fixture.calls, "xui.ensure") {
		t.Fatalf("legacy adoption rebuilt live resources: %#v", fixture.calls)
	}
}

func TestReconcileContinuesAfterOneCandidateFails(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "broken", CountryCode: "JP", CountryName: "日本", ProxyType: "datacenter", ProbeStatus: "available"},
		{ID: "healthy", CountryCode: "KR", CountryName: "韩国", ProxyType: "datacenter", ProbeStatus: "available"},
	}
	fixture.aimili.createErrors = map[string]error{"broken": &aimili.AdapterError{Code: "slot_create_failed"}}
	fixture.aimili.slotsByCandidate = map[string]aimili.Slot{
		"healthy": {Number: 1, Country: "KR", CountryName: "韩国", ProxyType: "datacenter", Port: 17929, Status: "up", NodeID: "healthy", ExitIP: "203.0.113.20", EgressOK: true},
	}

	result := fixture.orchestratorWithMax(t, 4).Reconcile(context.Background())

	if result.Ready != 1 || result.Failed != 1 {
		t.Fatalf("unexpected partial result: %#v", result)
	}
}

func TestReconcileKeepsOnlyTheBestCandidateForOneActualExit(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "jp-fast", CountryCode: "JP", CountryName: "日本", ProxyType: "datacenter", LatencyMS: 10, ProbeStatus: "available"},
		{ID: "jp-slow", CountryCode: "JP", CountryName: "日本", ProxyType: "datacenter", LatencyMS: 90, ProbeStatus: "available"},
	}
	fixture.aimili.slotsByCandidate = map[string]aimili.Slot{
		"jp-fast": {Number: 0, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17928, Status: "up", NodeID: "jp-fast", ExitIP: "203.0.113.50", EgressOK: true},
		"jp-slow": {Number: 1, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17929, Status: "up", NodeID: "jp-slow", ExitIP: "203.0.113.50", EgressOK: true},
	}

	result := fixture.orchestratorWithMax(t, 4).Reconcile(context.Background())
	groups, _ := fixture.store.ListProxyGroups(context.Background())

	if result.Ready != 1 || len(groups) != 1 || groups[0].CandidateID != "jp-fast" {
		t.Fatalf("duplicate actual exit was retained: result=%#v groups=%#v", result, groups)
	}
}
