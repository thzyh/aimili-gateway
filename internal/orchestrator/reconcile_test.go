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

func TestReconcileLeavesCandidatesBeyondCapacityOnStandby(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "jp-one", CountryCode: "JP", CountryName: "日本", IP: "198.51.100.10", ProxyType: "datacenter", LatencyMS: 21, ProbeStatus: "available"},
		{ID: "kr-two", CountryCode: "KR", CountryName: "韩国", IP: "198.51.100.11", ProxyType: "residential", LatencyMS: 35, ProbeStatus: "available"},
	}
	fixture.aimili.slotsByCandidate = map[string]aimili.Slot{
		"jp-one": {Number: 0, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17928, Status: "up", NodeID: "jp-one", ExitIP: "203.0.113.10", EgressOK: true},
		"kr-two": {Number: 1, Country: "KR", CountryName: "韩国", ProxyType: "residential", Port: 17929, Status: "up", NodeID: "kr-two", ExitIP: "203.0.113.11", EgressOK: true},
	}
	orchestrator := fixture.orchestratorWithMax(t, 1)

	result := orchestrator.Reconcile(context.Background())
	pool, err := orchestrator.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if result.Discovered != 2 || result.Ready != 1 || result.Failed != 0 {
		t.Fatalf("unexpected capacity-limited result: %#v", result)
	}
	if len(pool) != 2 || pool[0].Status != domain.ProxyGroupReady || pool[1].Status != domain.ProxyGroupStandby {
		t.Fatalf("standby candidate was not retained in the visible pool: %#v", pool)
	}
	if pool[0].ID == pool[1].ID || pool[1].ExitIP != "" || pool[1].PublicPort != 0 || pool[1].MixedPort != 0 {
		t.Fatalf("standby entry exposed live-only data: %#v", pool[1])
	}
}

func TestActivateStandbyCandidateReplacesTheSingleActiveGroup(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "jp-one", CountryCode: "JP", CountryName: "日本", IP: "198.51.100.10", ProxyType: "datacenter", LatencyMS: 21, ProbeStatus: "available"},
		{ID: "kr-two", CountryCode: "KR", CountryName: "韩国", IP: "198.51.100.11", ProxyType: "residential", LatencyMS: 35, ProbeStatus: "available"},
	}
	fixture.aimili.slotsByCandidate = map[string]aimili.Slot{
		"jp-one": {Number: 0, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17928, Status: "up", NodeID: "jp-one", ExitIP: "203.0.113.10", EgressOK: true},
		"kr-two": {Number: 1, Country: "KR", CountryName: "韩国", ProxyType: "residential", Port: 17929, Status: "up", NodeID: "kr-two", ExitIP: "203.0.113.11", EgressOK: true},
	}
	orchestrator := fixture.orchestratorWithMax(t, 1)
	orchestrator.Reconcile(context.Background())
	standby, _ := domain.NewProxyGroupIdentity("KR", domain.ProxyTypeResidential, "kr-two")

	activated, err := orchestrator.Activate(context.Background(), standby.ID)
	if err != nil {
		t.Fatal(err)
	}
	groups, _ := fixture.store.ListProxyGroups(context.Background())

	if activated.CandidateID != "kr-two" || activated.Status != domain.ProxyGroupReady {
		t.Fatalf("wrong candidate activated: %#v", activated)
	}
	if len(groups) != 1 || groups[0].CandidateID != "kr-two" {
		t.Fatalf("single-active capacity was not enforced: %#v", groups)
	}
	if !contains(fixture.calls, "slot.delete") || !contains(fixture.calls, "xui.delete") {
		t.Fatalf("previous active resources were not retired: %#v", fixture.calls)
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
	legacy.PublicPort = 20000
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

func TestReconcileAdoptsHealthyPrecreatedSlotsWithoutRecreatingThem(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{}
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		0: {Number: 0, Country: "KR", CountryName: "韩国", ProxyType: "residential", Port: 17928, Status: "up", NodeID: "runtime-slot-zero", CandidateIP: "198.51.100.10", ExitIP: "203.0.113.10", EgressOK: true, CheckedAt: 1_700_000_000},
		1: {Number: 1, Country: "KR", CountryName: "韩国", ProxyType: "residential", Port: 17929, Status: "up", NodeID: "runtime-slot-one", CandidateIP: "198.51.100.11", ExitIP: "203.0.113.11", EgressOK: true, CheckedAt: 1_700_000_001},
		2: {Number: 2, Country: "JP", CountryName: "日本", ProxyType: "residential", Port: 17930, Status: "up", NodeID: "runtime-slot-two", CandidateIP: "198.51.100.12", ExitIP: "203.0.113.12", EgressOK: true, CheckedAt: 1_700_000_002},
	}

	result := fixture.orchestratorWithMax(t, 3).Reconcile(context.Background())
	groups, err := fixture.store.ListProxyGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if result.Ready != 3 || result.Failed != 0 || len(groups) != 3 {
		t.Fatalf("precreated slots were not adopted: result=%#v groups=%#v", result, groups)
	}
	bySlot := make(map[int]domain.ProxyGroup, len(groups))
	for _, group := range groups {
		bySlot[group.AimiliSlot] = group
	}
	for slot := 0; slot < 3; slot++ {
		group, ok := bySlot[slot]
		if !ok || group.Status != domain.ProxyGroupReady || group.CandidateID == "" || group.PublicPort != 20000+slot || group.MixedPort != 30000+slot {
			t.Fatalf("adopted group %d is incomplete: %#v", slot, group)
		}
	}
	if contains(fixture.calls, "slot.create") || contains(fixture.calls, "slot.delete") {
		t.Fatalf("precreated slots were recreated or deleted: %#v", fixture.calls)
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

func TestReconcileRefreshesDegradedGroupAfterItsSlotChangesCandidate(t *testing.T) {
	fixture := newFixture()
	group, err := domain.NewProxyGroupIdentity("KR", domain.ProxyTypeDatacenter, "old-node")
	if err != nil {
		t.Fatal(err)
	}
	group.Status = domain.ProxyGroupDegraded
	group.LastErrorCode = "slot_not_found"
	group.AimiliSlot = 3
	group.PublicPort = 20003
	group.MixedPort = 30003
	group.PublicInboundID = 7
	group.MixedInboundID = 8
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{
		EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision,
		DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady,
	}
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "new-node", CountryCode: "TH", CountryName: "泰国", IP: "198.51.100.23", ProxyType: "residential", LatencyMS: 18, ProbeStatus: "available"},
	}
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		3: {Number: 3, Country: "TH", CountryName: "泰国", ProxyType: "residential", Port: 17931, Status: "up", NodeID: "new-node", CandidateIP: "198.51.100.23", ExitIP: "203.0.113.23", EgressOK: true, CheckedAt: 1_700_000_023},
	}

	result := fixture.orchestratorWithMax(t, 5).Reconcile(context.Background())
	refreshed := fixture.store.groups[group.ID]

	if result.Ready != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if refreshed.Status != domain.ProxyGroupReady || refreshed.CandidateID != "new-node" || refreshed.AimiliSlot != 3 || refreshed.LastErrorCode != "" {
		t.Fatalf("degraded group was not refreshed from its current slot: %#v", refreshed)
	}
	if !contains(fixture.calls, "slot.check") {
		t.Fatalf("current slot was not checked: %#v", fixture.calls)
	}
}

func TestReconcileRefreshesStaleManagedResourceFailureAfterSlotRecovers(t *testing.T) {
	for _, errorCode := range []string{"managed_resource_drift", "rollback_failed"} {
		t.Run(errorCode, func(t *testing.T) {
			fixture := newFixture()
			group, err := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "jp-node")
			if err != nil {
				t.Fatal(err)
			}
			group.Status = domain.ProxyGroupDegraded
			group.LastErrorCode = errorCode
			group.AimiliSlot = 1
			group.PublicPort = 20001
			group.MixedPort = 30001
			group.PublicInboundID = 7
			group.MixedInboundID = 8
			fixture.store.groups[group.ID] = group
			fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{
				EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision,
				DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady,
			}
			fixture.aimili.candidates = []aimili.Candidate{{ID: "jp-node", CountryCode: "JP", CountryName: "日本", ProxyType: "datacenter", ProbeStatus: "available"}}
			fixture.aimili.createdSlots = map[int]aimili.Slot{
				1: {Number: 1, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Port: 17929, Status: "up", NodeID: "jp-node", ExitIP: "203.0.113.21", EgressOK: true},
			}

			result := fixture.orchestratorWithMax(t, 3).Reconcile(context.Background())
			refreshed := fixture.store.groups[group.ID]
			if result.Ready != 1 || result.Failed != 0 || refreshed.Status != domain.ProxyGroupReady || refreshed.LastErrorCode != "" {
				t.Fatalf("healthy runtime did not clear stale failure: result=%#v group=%#v", result, refreshed)
			}
			if !contains(fixture.calls, "slot.check") {
				t.Fatalf("healthy runtime was not revalidated: %#v", fixture.calls)
			}
		})
	}
}

func TestReconcilePassivelySynchronizesManualRepairStateWithoutCheckingAgain(t *testing.T) {
	fixture := newFixture()
	group, err := domain.NewProxyGroupIdentity("RU", domain.ProxyTypeDatacenter, "ru-old")
	if err != nil {
		t.Fatal(err)
	}
	group.Status = domain.ProxyGroupDegraded
	group.LastErrorCode = "egress_check_failed"
	group.AimiliSlot = 0
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.PublicInboundID = 7
	group.MixedInboundID = 8
	fixture.store.groups[group.ID] = group
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "ru-old", CountryCode: "RU", CountryName: "俄罗斯", ProxyType: "datacenter", ProbeStatus: "available"},
	}
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		0: {
			Number: 0, Country: "RU", CountryName: "俄罗斯", ProxyType: "datacenter",
			Status: "disconnected", NodeID: "ru-old", EgressOK: false,
			RepairStatus: "manual_required", AutoRepairAttempted: true,
			LastErrorCode: "no_same_country_candidate",
		},
	}

	result := fixture.orchestratorWithMax(t, 3).Reconcile(context.Background())
	refreshed := fixture.store.groups[group.ID]

	if result.Ready != 0 || result.Failed != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if refreshed.Status != domain.ProxyGroupDegraded || refreshed.LastErrorCode != "no_same_country_candidate" {
		t.Fatalf("manual repair state was not synchronized: %#v", refreshed)
	}
	if contains(fixture.calls, "slot.check") {
		t.Fatalf("passive synchronization triggered another repair-capable check: %#v", fixture.calls)
	}
	version := refreshed.Version
	fixture.calls = nil
	fixture.orchestratorWithMax(t, 3).Reconcile(context.Background())
	if again := fixture.store.groups[group.ID]; again.Version != version {
		t.Fatalf("unchanged manual repair state was written again: before=%d after=%d", version, again.Version)
	}
	if contains(fixture.calls, "slot.check") {
		t.Fatalf("repeat reconciliation triggered another repair-capable check: %#v", fixture.calls)
	}
}

func TestReconcileRefreshesReadyGroupsAfterRuntimeCandidateSwap(t *testing.T) {
	fixture := newFixture()
	first, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "candidate-one")
	second, _ := domain.NewProxyGroupIdentity("KR", domain.ProxyTypeDatacenter, "candidate-two")
	for index, group := range []*domain.ProxyGroup{&first, &second} {
		group.Status = domain.ProxyGroupReady
		group.AimiliSlot = index
		group.PublicPort = 20000 + index
		group.MixedPort = 30000 + index
		group.PublicInboundID = int64(10 + index*2)
		group.MixedInboundID = int64(11 + index*2)
		group.Version = 1
		fixture.store.groups[group.ID] = *group
		fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{
			EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision,
			DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady,
		}
	}
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "candidate-one", CountryCode: "JP", ProxyType: "datacenter", ProbeStatus: "available"},
		{ID: "candidate-two", CountryCode: "KR", ProxyType: "datacenter", ProbeStatus: "available"},
	}
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		0: {Number: 0, Country: "KR", CountryName: "韩国", ProxyType: "datacenter", Status: "up", NodeID: "candidate-two", ExitIP: "203.0.113.20", EgressOK: true},
		1: {Number: 1, Country: "JP", CountryName: "日本", ProxyType: "datacenter", Status: "up", NodeID: "candidate-one", ExitIP: "203.0.113.21", EgressOK: true},
	}

	result := fixture.orchestratorWithMax(t, 2).Reconcile(context.Background())
	updatedFirst := fixture.store.groups[first.ID]
	updatedSecond := fixture.store.groups[second.ID]
	if result.Failed != 0 || result.Ready != 2 || updatedFirst.CandidateID != "candidate-two" || updatedSecond.CandidateID != "candidate-one" {
		t.Fatalf("result=%#v first=%#v second=%#v", result, updatedFirst, updatedSecond)
	}
	if !contains(fixture.calls, "slot.check") {
		t.Fatalf("runtime-swapped groups were not checked: %#v", fixture.calls)
	}
}

func TestReconcileReprovisionsMissingManagedResourcesForCurrentSlot(t *testing.T) {
	fixture := newFixture()
	group, err := domain.NewProxyGroupIdentity("KR", domain.ProxyTypeDatacenter, "old-node")
	if err != nil {
		t.Fatal(err)
	}
	group.Status = domain.ProxyGroupDegraded
	group.LastErrorCode = "protocol_failed"
	group.AimiliSlot = 4
	group.PublicPort = 20004
	group.MixedPort = 30004
	fixture.store.groups[group.ID] = group
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "current-node", CountryCode: "VN", CountryName: "越南", IP: "198.51.100.24", ProxyType: "residential", LatencyMS: 12, ProbeStatus: "available"},
	}
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		4: {Number: 4, Country: "VN", CountryName: "越南", ProxyType: "residential", Port: 17932, Status: "up", NodeID: "current-node", CandidateIP: "198.51.100.24", ExitIP: "203.0.113.24", EgressOK: true, CheckedAt: 1_700_000_024},
	}

	result := fixture.orchestratorWithMax(t, 5).Reconcile(context.Background())
	refreshed := fixture.store.groups[group.ID]

	if result.Ready != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if refreshed.Status != domain.ProxyGroupReady || refreshed.CandidateID != "current-node" || refreshed.PublicInboundID <= 0 || refreshed.MixedInboundID <= 0 {
		t.Fatalf("missing managed resources were not reprovisioned: %#v", refreshed)
	}
	mode, ok := fixture.store.protocolModes[group.ID]
	if !ok || mode.State != domain.ProtocolReady {
		t.Fatalf("protocol state was not initialized: %#v", mode)
	}
}

func TestReconcileRotatesANewDuplicateExitInsteadOfRemovingEitherCandidate(t *testing.T) {
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

	if result.Ready != 2 || len(groups) != 2 || groups[0].ExitIP == groups[1].ExitIP || !contains(fixture.calls, "slot.rotate") {
		t.Fatalf("new duplicate exit was not rotated: result=%#v groups=%#v calls=%#v", result, groups, fixture.calls)
	}
}

func TestReconcileDegradesOnlyTheNewerHistoricalDuplicate(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{
		{ID: "older", CountryCode: "JP", CountryName: "日本", ProxyType: "datacenter", ProbeStatus: "available"},
		{ID: "newer", CountryCode: "US", CountryName: "美国", ProxyType: "datacenter", ProbeStatus: "available"},
	}
	older, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "older")
	older.Status = domain.ProxyGroupReady
	older.ExitIP = "203.0.113.80"
	older.CreatedAt = time.Unix(1_699_999_000, 0).UTC()
	older.UpdatedAt = older.CreatedAt
	newer, _ := domain.NewProxyGroupIdentity("US", domain.ProxyTypeDatacenter, "newer")
	newer.Status = domain.ProxyGroupReady
	newer.ExitIP = "203.0.113.80"
	newer.CreatedAt = time.Unix(1_699_999_500, 0).UTC()
	newer.UpdatedAt = newer.CreatedAt
	fixture.store.groups[older.ID] = older
	fixture.store.groups[newer.ID] = newer

	result := fixture.orchestratorWithMax(t, 3).Reconcile(context.Background())
	storedOlder := fixture.store.groups[older.ID]
	storedNewer := fixture.store.groups[newer.ID]
	if result.Ready != 1 || result.Failed != 1 || storedOlder.Status != domain.ProxyGroupReady || storedNewer.Status != domain.ProxyGroupDegraded || storedNewer.LastErrorCode != "duplicate_exit_ip" {
		t.Fatalf("result=%#v older=%#v newer=%#v", result, storedOlder, storedNewer)
	}
	if contains(fixture.calls, "slot.delete") || contains(fixture.calls, "xui.delete") {
		t.Fatalf("historical duplicate resources were deleted: %#v", fixture.calls)
	}
}
