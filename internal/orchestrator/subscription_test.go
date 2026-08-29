package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

func TestSubscriptionIncludesMainAndReadyManagedVLESSOnly(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "jp-ready")
	group.Status = domain.ProxyGroupReady
	group.PublicInboundID = 11
	group.MixedInboundID = 12
	group.AimiliSlot = 0
	group.PublicPort = 20000
	group.MixedPort = 30000
	fixture.store.groups[group.ID] = group
	fixture.xui.snapshot = xui.Snapshot{Inbounds: []xui.Inbound{
		{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443},
		{ID: 11, Tag: group.ResourceName + "-vless", Remark: "Aimili Gateway " + group.ResourceName + " VLESS", Protocol: "vless", Port: 20000},
		{ID: 12, Tag: group.ResourceName + "-mixed", Remark: "Aimili Gateway " + group.ResourceName + " mixed", Protocol: "mixed", Port: 30000},
	}}
	result, err := fixture.orchestratorWithMax(t, 3).Subscription(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.InboundCount != 2 || !strings.HasPrefix(result.URL, "https://proxy.example.test/") {
		t.Fatalf("result=%#v", result)
	}
	if got := fixture.xui.subscriptionDesired.InboundIDs; len(got) != 2 || got[0] != 1 || got[1] != 11 {
		t.Fatalf("desired inbound IDs=%v", got)
	}
}

func TestCleanupLegacyAggregateRequiresExactOwnedResourceAndSubscriptionCoverage(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "jp-ready")
	group.Status = domain.ProxyGroupReady
	group.PublicInboundID = 11
	fixture.store.groups[group.ID] = group
	fixture.store.aggregate = store.AggregateConfig{ResourceName: "agw-aggregate-vless", VLESSInboundID: 9, VLESSPort: 21000, Enabled: true, UpdatedAt: fixture.now()}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: []xui.Inbound{
		{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443},
		{ID: 11, Tag: group.ResourceName + "-vless", Remark: "Aimili Gateway " + group.ResourceName + " VLESS", Protocol: "vless", Port: 20000},
	}}

	result, err := fixture.orchestratorWithMax(t, 3).CleanupLegacyAggregate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed || fixture.store.aggregate.Enabled || fixture.xui.deletedAggregate.VLESSInboundID != 9 || fixture.xui.deletedAggregate.VLESSPort != 21000 {
		t.Fatalf("cleanup result=%#v aggregate=%#v deleted=%#v", result, fixture.store.aggregate, fixture.xui.deletedAggregate)
	}
}

func TestCleanupLegacyAggregateRejectsHistoricalConfigDrift(t *testing.T) {
	fixture := newFixture()
	fixture.store.aggregate = store.AggregateConfig{ResourceName: "user-resource", VLESSInboundID: 9, VLESSPort: 21000, Enabled: true, UpdatedAt: fixture.now()}

	_, err := fixture.orchestratorWithMax(t, 3).CleanupLegacyAggregate(context.Background())
	if codeOf(err) != "ownership_conflict" || fixture.xui.deletedAggregate.VLESSInboundID != 0 || !fixture.store.aggregate.Enabled {
		t.Fatalf("error=%v aggregate=%#v deleted=%#v", err, fixture.store.aggregate, fixture.xui.deletedAggregate)
	}
}

func TestCleanupLegacyAggregateDoesNotDisableStateAfterPartialDelete(t *testing.T) {
	fixture := newFixture()
	fixture.store.aggregate = store.AggregateConfig{ResourceName: "agw-aggregate-vless", VLESSInboundID: 9, VLESSPort: 21000, Enabled: true, UpdatedAt: fixture.now()}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}}
	fixture.xui.deleteAggregateError = &xui.AdapterError{Code: "partial_delete"}

	_, err := fixture.orchestratorWithMax(t, 3).CleanupLegacyAggregate(context.Background())
	if codeOf(err) != "repair_required" || !fixture.store.aggregate.Enabled {
		t.Fatalf("error=%v aggregate=%#v", err, fixture.store.aggregate)
	}
}

func TestReplaceCandidateAssignsExistingSlotAndKeepsStablePorts(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "old-node")
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
	fixture.aimili.createdSlots = map[int]aimili.Slot{2: {Number: 2, NodeID: "old-node", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.7", Port: 17932, Status: "up", EgressOK: true}}
	fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "KR", CountryName: "韩国", ProxyType: "datacenter", ProbeStatus: "available", IP: "198.51.100.8", LatencyMS: 45}}
	fixture.aimili.assignedSlot = aimili.Slot{Number: 2, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", ExitIP: "203.0.113.8", Port: 17932, Status: "up", EgressOK: true}
	updated, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CandidateID != "new-node" || updated.AimiliSlot != 2 || updated.PublicPort != 20000 || updated.MixedPort != 30000 || updated.ExitIP != "203.0.113.8" {
		t.Fatalf("updated=%#v", updated)
	}
}

func TestReplaceCandidateWaitsForAssignedSlotEgress(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "old-node")
	group.Status = domain.ProxyGroupReady
	group.AimiliSlot = 2
	group.CandidateID = "old-node"
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.ExitIP = "203.0.113.7"
	group.RealityPublicKey = "pk"
	group.RealityShortID = "sid"
	group.RealityServerName = "proxy.example.test"
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.aimili.createdSlots = map[int]aimili.Slot{2: {Number: 2, NodeID: "runtime-old", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.7", Port: 17932, Status: "up", EgressOK: true}}
	fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "KR", CountryName: "韩国", ProxyType: "datacenter", ProbeStatus: "available", IP: "198.51.100.8", LatencyMS: 45}}
	fixture.aimili.assignedSlot = aimili.Slot{Number: 2, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", Port: 17932, Status: "starting", EgressOK: false}
	fixture.aimili.checkResults = []aimili.SlotCheck{
		{Number: 2, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", Port: 17932, Status: "starting", EgressOK: false},
		{Number: 2, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", ExitIP: "203.0.113.8", Port: 17932, Status: "up", EgressOK: true},
	}

	updated, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CandidateID != "new-node" || updated.ExitIP != "203.0.113.8" || !contains(fixture.calls, "slot.check") {
		t.Fatalf("assigned slot was not awaited: %#v calls=%v", updated, fixture.calls)
	}
}

func TestReplaceCandidateMarksRepairWhenRuntimeRollbackFails(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "stale-db-node")
	group.Status = domain.ProxyGroupReady
	group.AimiliSlot = 2
	group.CandidateID = "stale-db-node"
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.ExitIP = "203.0.113.7"
	group.RealityPublicKey = "pk"
	group.RealityShortID = "sid"
	group.RealityServerName = "proxy.example.test"
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.aimili.createdSlots = map[int]aimili.Slot{2: {Number: 2, NodeID: "runtime-old", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.7", Port: 17932, Status: "up", EgressOK: true}}
	fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "KR", CountryName: "韩国", ProxyType: "datacenter", ProbeStatus: "available", IP: "198.51.100.8", LatencyMS: 45}}
	fixture.aimili.assignedSlots = []aimili.Slot{{Number: 2, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", ExitIP: "203.0.113.8", Port: 17932, Status: "up", EgressOK: true}}
	fixture.aimili.assignErrors = []error{nil, &aimili.AdapterError{Code: "candidate_unavailable"}}
	fixture.validator.vlessError = &validator.Error{Code: "protocol_failed"}

	_, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", group.ID)
	if codeOf(err) != "repair_required" {
		t.Fatalf("error=%v", err)
	}
	stored := fixture.store.groups[group.ID]
	if stored.Status != domain.ProxyGroupRepairRequired || stored.LastErrorCode != "rollback_failed" || len(fixture.aimili.assignRequests) != 2 || fixture.aimili.assignRequests[1].CandidateID != "runtime-old" {
		t.Fatalf("rollback state=%#v requests=%#v", stored, fixture.aimili.assignRequests)
	}
}

func TestReplaceCandidateRejectsMainAndStandbyTarget(t *testing.T) {
	fixture := newFixture()
	o := fixture.orchestratorWithMax(t, 3)
	if _, err := o.ReplaceCandidate(context.Background(), "candidate", "agw-main"); codeOf(err) != "invalid_request" {
		t.Fatalf("main error=%v", err)
	}
	if _, err := o.ReplaceCandidate(context.Background(), "candidate", "missing"); codeOf(err) != "not_found" {
		t.Fatalf("missing error=%v", err)
	}
}

func TestCheckMainStoresBothProtocolLatencies(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainStatus = aimili.MainStatus{Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true}
	fixture.validator.socksLatency = 12 * time.Millisecond
	fixture.validator.vlessLatency = 18 * time.Millisecond
	main, err := fixture.orchestratorWithMax(t, 3).CheckMain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if main.SOCKSLatencyMS != 12 || main.VLESSLatencyMS != 18 || !main.Enabled {
		t.Fatalf("main=%#v", main)
	}
}

func TestCheckMainWaitsForBothProtocolsAfterXrayReload(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainStatus = aimili.MainStatus{Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true}
	fixture.validator.socksErrors = []error{&validator.Error{Code: "connection_failed"}, nil}
	fixture.validator.vlessErrors = []error{&validator.Error{Code: "protocol_failed"}, nil}
	fixture.validator.socksLatency = 12 * time.Millisecond
	fixture.validator.vlessLatency = 18 * time.Millisecond
	main, err := fixture.orchestratorWithMax(t, 3).CheckMain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if main.SOCKSLatencyMS != 12 || main.VLESSLatencyMS != 18 || fixture.validator.socksCalls != 2 || fixture.validator.vlessCalls != 2 {
		t.Fatalf("main=%#v socksCalls=%d vlessCalls=%d", main, fixture.validator.socksCalls, fixture.validator.vlessCalls)
	}
}
