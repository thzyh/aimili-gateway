package orchestrator

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

func TestRotateMixedCredentialsUpdatesFaultedGroupsAndValidatesOnlyHealthyExits(t *testing.T) {
	fixture := newFixture()
	healthy := mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1)
	faulted := mixedPolicyGroup("agw-us-res-b", 20002, 30002, 2)
	faulted.Status = domain.ProxyGroupDegraded
	fixture.store.groups = map[string]domain.ProxyGroup{healthy.ID: healthy, faulted.ID: faulted}
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "SG", ProxyType: domain.ProxyTypeDatacenter,
		ExitIP: "203.0.113.9", PublicInboundID: 91, MixedInboundID: 92, PublicPort: 8443, MixedPort: 31000,
		Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		1: {Number: 1, Port: 17931, EgressOK: true, Status: "up"},
		2: {Number: 2, Port: 17932, EgressOK: false, Status: "disconnected"},
	}

	rotatedAt, err := fixture.orchestratorWithMax(t, 2).RotateMixedCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rotatedAt.Equal(fixture.now()) {
		t.Fatalf("rotatedAt = %v", rotatedAt)
	}
	username := string(fixture.store.credentials[credentialMixedUsername])
	password := string(fixture.store.credentials[credentialMixedPassword])
	if !strings.HasPrefix(username, "agw-") || len(username) != 20 || len(password) != 48 || username == "proxy-user" || password == "proxy-password" {
		t.Fatalf("unexpected generated credential shape: username length=%d password length=%d", len(username), len(password))
	}
	if !equalStrings(fixture.xui.updateNames, []string{healthy.ID, faulted.ID}) || fixture.validator.socksCalls != 1 {
		t.Fatalf("updates=%#v validations=%d", fixture.xui.updateNames, fixture.validator.socksCalls)
	}
	if fixture.xui.ensureLegacyMainCalls != 1 || fixture.xui.legacyMainDesired.MixedUsername != username || fixture.xui.legacyMainDesired.MixedPassword != password {
		t.Fatal("faulted main mixed inbound did not receive the rotated credentials")
	}
	for _, desired := range fixture.xui.updated {
		if desired.MixedUsername != username || desired.MixedPassword != password {
			t.Fatal("3x-ui did not receive the committed credential pair")
		}
	}
}

func TestRotateMixedCredentialsRollsBackEveryAppliedInboundBeforeKeepingOldPair(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = mixedPolicyGroups()
	fixture.aimili.createdSlots = map[int]aimili.Slot{
		1: {Number: 1, Port: 17931, EgressOK: true, Status: "up"},
		2: {Number: 2, Port: 17932, EgressOK: true, Status: "up"},
	}
	fixture.xui.updateErrors = map[int]error{2: errors.New("second update failed")}

	err := func() error {
		_, err := fixture.orchestratorWithMax(t, 2).RotateMixedCredentials(context.Background())
		return err
	}()
	if codeOf(err) != "mixed_credentials_apply_failed" {
		t.Fatalf("error = %v", err)
	}
	if string(fixture.store.credentials[credentialMixedUsername]) != "proxy-user" || string(fixture.store.credentials[credentialMixedPassword]) != "proxy-password" {
		t.Fatal("failed rotation changed stored credentials")
	}
	if !equalStrings(fixture.xui.updateNames, []string{"agw-jp-dc-a", "agw-us-res-b", "agw-us-res-b", "agw-jp-dc-a"}) {
		t.Fatalf("apply and rollback order = %#v", fixture.xui.updateNames)
	}
}

func TestRotateMixedCredentialsRollsBackWhenAHealthyExitRejectsTheNewPair(t *testing.T) {
	fixture := newFixture()
	group := mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1)
	fixture.store.groups = map[string]domain.ProxyGroup{group.ID: group}
	fixture.aimili.createdSlots = map[int]aimili.Slot{1: {Number: 1, Port: 17931, EgressOK: true, Status: "up"}}
	fixture.validator.socksErrors = []error{&validator.Error{Code: "proxy_auth_failed"}}

	_, err := fixture.orchestrator(t).RotateMixedCredentials(context.Background())
	if codeOf(err) != "mixed_credentials_apply_failed" {
		t.Fatalf("error = %v", err)
	}
	if !equalStrings(fixture.xui.updateNames, []string{group.ID, group.ID}) {
		t.Fatalf("apply and rollback order = %#v", fixture.xui.updateNames)
	}
	if string(fixture.store.credentials[credentialMixedUsername]) != "proxy-user" || string(fixture.store.credentials[credentialMixedPassword]) != "proxy-password" {
		t.Fatal("failed validation changed stored credentials")
	}
}

func TestSetMixedPolicyAppliesEveryGroupInStableOrder(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = mixedPolicyGroups()
	fixture.store.policy = store.MixedSourcePolicy{Enabled: false, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}

	wanted := store.MixedSourcePolicy{
		Enabled: true,
		CIDRs: []netip.Prefix{
			netip.MustParsePrefix("198.51.100.0/24"),
		},
	}
	if err := fixture.orchestratorWithMax(t, 2).SetMixedPolicy(context.Background(), wanted); err != nil {
		t.Fatal(err)
	}
	if got := fixture.xui.updateNames; !equalStrings(got, []string{"agw-jp-dc-a", "agw-us-res-b"}) {
		t.Fatalf("update order = %#v", got)
	}
	for _, desired := range fixture.xui.updated {
		if !desired.MixedSourceRestrictionEnabled || !equalStrings(desired.MixedSourceCIDRs, []string{"198.51.100.0/24"}) {
			t.Fatalf("desired policy = %#v", desired)
		}
	}
	if fixture.validator.socksCalls != 2 {
		t.Fatalf("SOCKS validations = %d", fixture.validator.socksCalls)
	}
	if !fixture.store.policy.Enabled || fixture.store.policy.ApplyStatus != store.MixedPolicyApplied {
		t.Fatalf("stored policy = %#v", fixture.store.policy)
	}
}

func TestSetMixedPolicyDisablesWhitelistButKeepsAuthenticatedMixedRouting(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = mixedPolicyGroups()
	fixture.store.policy = store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}

	if err := fixture.orchestratorWithMax(t, 2).SetMixedPolicy(context.Background(), store.MixedSourcePolicy{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	for _, desired := range fixture.xui.updated {
		if desired.MixedSourceRestrictionEnabled || len(desired.MixedSourceCIDRs) != 0 || desired.MixedUsername == "" || desired.MixedPassword == "" {
			t.Fatalf("disabled desired group = %#v", desired)
		}
	}
	if fixture.store.policy.Enabled || fixture.store.policy.ApplyStatus != store.MixedPolicyApplied {
		t.Fatalf("stored policy = %#v", fixture.store.policy)
	}
}

func TestSetMixedPolicyUpdatesEnabledMainMixedSourceRules(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter,
		CandidateID: "main-candidate", ExitIP: "203.0.113.6", PublicInboundID: 1, MixedInboundID: 98,
		PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.store.policy = store.MixedSourcePolicy{Enabled: false, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}

	if err := fixture.orchestrator(t).SetMixedPolicy(context.Background(), store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}}); err != nil {
		t.Fatal(err)
	}
	if fixture.xui.ensureLegacyMainCalls != 1 {
		t.Fatalf("main mixed update calls = %d", fixture.xui.ensureLegacyMainCalls)
	}
	want := xui.LegacyMainDesired{VLESSPort: 8443, MixedPort: 31000, SOCKSPort: 7928, MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"}, RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test"}
	got := fixture.xui.legacyMainDesired
	if got.VLESSPort != want.VLESSPort || got.MixedPort != want.MixedPort || got.SOCKSPort != want.SOCKSPort || got.MixedSourceRestrictionEnabled != want.MixedSourceRestrictionEnabled || !equalStrings(got.MixedSourceCIDRs, want.MixedSourceCIDRs) || got.RealityTarget != want.RealityTarget || got.RealityServerName != want.RealityServerName || got.MixedUsername == "" || got.MixedPassword == "" {
		t.Fatalf("main mixed source rules = %#v", got)
	}
}

func TestSetMixedPolicyRollsBackAppliedGroupsWhenSecondValidationFails(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = mixedPolicyGroups()
	fixture.store.policy = store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}
	fixture.validator.socksErrors = []error{nil, &validator.Error{Code: "proxy_dns_failed"}, nil, nil}

	err := fixture.orchestratorWithMax(t, 2).SetMixedPolicy(context.Background(), store.MixedSourcePolicy{Enabled: false})
	if codeOf(err) != "mixed_policy_apply_failed" {
		t.Fatalf("error = %v", err)
	}
	if got := fixture.xui.updateNames; !equalStrings(got, []string{"agw-jp-dc-a", "agw-us-res-b", "agw-us-res-b", "agw-jp-dc-a"}) {
		t.Fatalf("apply and rollback order = %#v", got)
	}
	for _, group := range fixture.store.groups {
		if group.ConfigFingerprint != "old-"+group.ID {
			t.Fatalf("fingerprint was not restored: %#v", group)
		}
	}
	if !fixture.store.policy.Enabled || fixture.store.policy.ApplyStatus != store.MixedPolicyFailed {
		t.Fatalf("rolled back policy = %#v", fixture.store.policy)
	}
}

func TestSetMixedPolicyMarksRepairRequiredWhenRollbackFails(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = mixedPolicyGroups()
	fixture.store.policy = store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}
	fixture.validator.socksErrors = []error{nil, &validator.Error{Code: "proxy_dns_failed"}}
	fixture.xui.updateErrors = map[int]error{3: errors.New("rollback failed")}

	err := fixture.orchestratorWithMax(t, 2).SetMixedPolicy(context.Background(), store.MixedSourcePolicy{Enabled: false})
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	if fixture.store.policy.ApplyStatus != store.MixedPolicyRepairRequired {
		t.Fatalf("policy = %#v", fixture.store.policy)
	}
}

func TestRepairManagedReappliesCurrentPolicyOnlyToStoredGroups(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = mixedPolicyGroups()
	setReadyRepairModes(fixture)
	fixture.store.policy = store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}
	if err := fixture.orchestratorWithMax(t, 2).RepairManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !equalStrings(fixture.xui.updateNames, []string{"agw-jp-dc-a", "agw-us-res-b"}) || fixture.validator.socksCalls != 2 {
		t.Fatalf("updates=%#v validations=%d", fixture.xui.updateNames, fixture.validator.socksCalls)
	}
	for _, group := range fixture.store.groups {
		if group.ConfigFingerprint != "new-"+group.ID {
			t.Fatalf("group was not repaired: %#v", group)
		}
	}
}

func TestRepairManagedRecoversMissingActiveMainBeforeReportingSuccess(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "main-candidate", Country: "JP", CountryName: "日本", ProxyType: "datacenter",
		ExitIP: "203.0.113.8", Port: 7928, EgressOK: true, Active: true,
	}
	if err := fixture.orchestrator(t).RepairManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fixture.store.mainEgress.Enabled || fixture.store.mainEgress.PublicInboundID != 1 || fixture.store.mainEgress.MixedInboundID != 98 || fixture.xui.ensureLegacyMainCalls == 0 {
		t.Fatalf("missing main was not recovered: main=%#v calls=%d", fixture.store.mainEgress, fixture.xui.ensureLegacyMainCalls)
	}
}

func TestRepairManagedSyncsSwitchedMainBeforeApplyingPolicy(t *testing.T) {
	fixture := newFixture()
	fixture.xui.snapshot.Inbounds = []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter,
		CandidateID: "old-main", ExitIP: "203.0.113.7", PublicInboundID: 1, MixedInboundID: 98,
		PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{
		EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSTCPRealityVision,
		DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now(),
	}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "日本", ProxyType: "residential",
		ExitIP: "203.0.113.8", Port: 7928, EgressOK: true, Active: true,
	}
	if err := fixture.orchestrator(t).RepairManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.store.mainEgress.CandidateID != "new-main" || fixture.store.mainEgress.ExitIP != "203.0.113.8" || fixture.store.policy.ApplyStatus != store.MixedPolicyApplied {
		t.Fatalf("main was not synchronized before policy apply: main=%#v policy=%#v", fixture.store.mainEgress, fixture.store.policy)
	}
	if len(fixture.validator.socksExpectedIPs) == 0 || fixture.validator.socksExpectedIPs[0] != "203.0.113.8" {
		t.Fatalf("main validation used a stale exit: %#v", fixture.validator.socksExpectedIPs)
	}
}

func TestRepairManagedLeavesPolicyUntouchedWhenSwitchedMainFailsValidation(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter,
		CandidateID: "old-main", ExitIP: "203.0.113.7", PublicInboundID: 1, MixedInboundID: 98,
		PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{
		EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSTCPRealityVision,
		DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now(),
	}
	fixture.xui.snapshot.Inbounds = []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "日本", ProxyType: "residential",
		ExitIP: "203.0.113.8", Port: 7928, EgressOK: true, Active: true,
	}
	fixture.validator.publicErrors = []error{&validator.Error{Code: "proxy_auth_failed"}}
	if err := fixture.orchestrator(t).RepairManaged(context.Background()); err == nil {
		t.Fatal("unverified main was accepted")
	}
	if fixture.store.policy.ApplyStatus != store.MixedPolicyApplied || fixture.store.mainEgress.CandidateID != "old-main" || len(fixture.xui.updateNames) != 0 {
		t.Fatalf("failed main check changed policy or resources: policy=%#v main=%#v updates=%v", fixture.store.policy, fixture.store.mainEgress, fixture.xui.updateNames)
	}
}

func TestRepairManagedPersistsRealityMaterialReturnedByXUI(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = map[string]domain.ProxyGroup{
		"agw-jp-dc-a": mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1),
	}
	setReadyRepairModes(fixture)
	fixture.store.policy = store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}
	fixture.xui.returnedPublicKey = "current-public-key"
	fixture.xui.returnedShortID = "current-short-id"
	fixture.xui.returnedServerName = "proxy.example.test"
	fixture.xui.returnedVLESSInboundID = 51
	fixture.xui.returnedMixedInboundID = 52
	fixture.xui.returnedResourceName = "agw-jp-dc-previous"

	if err := fixture.orchestrator(t).RepairManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	group := fixture.store.groups["agw-jp-dc-a"]
	if group.RealityPublicKey != "current-public-key" || group.RealityShortID != "current-short-id" || group.RealityServerName != "proxy.example.test" ||
		group.PublicInboundID != 51 || group.MixedInboundID != 52 || group.ResourceName != "agw-jp-dc-previous" {
		t.Fatalf("repaired Reality material was not persisted: %#v", group)
	}
}

func TestRepairManagedValidatesAndPersistsTheFreshAimiliExit(t *testing.T) {
	fixture := newFixture()
	group := mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1)
	group.ExitIP = "203.0.113.6"
	fixture.store.groups = map[string]domain.ProxyGroup{group.ID: group}
	setReadyRepairModes(fixture)
	fixture.store.policy = store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}
	fixture.aimili.createdSlots = map[int]aimili.Slot{}
	fixture.aimili.checkResults = []aimili.SlotCheck{{Number: 1, Country: "JP", ProxyType: "datacenter", Port: 17930, Status: "up", ExitIP: "203.0.113.7", EgressOK: true, CheckedAt: 1_700_000_020.5}}

	if err := fixture.orchestrator(t).RepairManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !equalStrings(fixture.validator.socksExpectedIPs, []string{"203.0.113.7"}) {
		t.Fatalf("SOCKS validation did not use the fresh Aimili exit: %#v", fixture.validator.socksExpectedIPs)
	}
	if stored := fixture.store.groups[group.ID]; stored.ExitIP != "203.0.113.7" || stored.ExitIPCheckedAt != 1_700_000_020.5 {
		t.Fatalf("fresh Aimili exit was not persisted: %#v", stored)
	}
}

func TestRepairManagedRestoresPublicResourceAndSynchronizesFreshSlotIdentity(t *testing.T) {
	fixture := newFixture()
	group := mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1)
	group.CandidateID = "stale-candidate"
	group.CountryCode = "VN"
	group.CountryName = "越南"
	group.ProxyType = domain.ProxyTypeResidential
	fixture.store.groups = map[string]domain.ProxyGroup{group.ID: group}
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{
		EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision,
		DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady,
		Version: 1, UpdatedAt: fixture.now(),
	}
	fixture.store.policy = store.MixedSourcePolicy{Enabled: false, ApplyStatus: store.MixedPolicyApplied, UpdatedAt: fixture.now()}
	fixture.aimili.createdSlots = map[int]aimili.Slot{}
	fixture.aimili.checkResults = []aimili.SlotCheck{{
		NodeID: "fresh-candidate", Country: "KR", CountryName: "韩国", ProxyType: "datacenter",
		Port: 17931, Status: "up", ExitIP: "203.0.113.9", EgressOK: true, CheckedAt: 1_700_000_030.5,
	}}
	fixture.xui.returnedPublicKey = "fresh-public-key"
	fixture.xui.returnedShortID = "fresh-short-id"
	fixture.xui.returnedServerName = "proxy.example.test"
	fixture.xui.returnedVLESSInboundID = 51

	if err := fixture.orchestrator(t).RepairManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !equalStrings(fixture.xui.repairPublicNames, []string{group.ResourceName}) {
		t.Fatalf("public repairs = %#v", fixture.xui.repairPublicNames)
	}
	stored := fixture.store.groups[group.ID]
	if stored.CandidateID != "fresh-candidate" || stored.CountryCode != "KR" || stored.CountryName != "韩国" ||
		stored.ProxyType != domain.ProxyTypeDatacenter || stored.ExitIP != "203.0.113.9" || stored.PublicInboundID != 51 ||
		stored.RealityPublicKey != "fresh-public-key" || stored.RealityShortID != "fresh-short-id" {
		t.Fatalf("synchronized group = %#v", stored)
	}
}

func mixedPolicyGroups() map[string]domain.ProxyGroup {
	return map[string]domain.ProxyGroup{
		"agw-us-res-b": mixedPolicyGroup("agw-us-res-b", 20002, 30002, 2),
		"agw-jp-dc-a":  mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1),
	}
}

func setReadyRepairModes(fixture *fixture) {
	for id := range fixture.store.groups {
		fixture.store.protocolModes[id] = domain.EgressProtocolMode{
			EgressID: id, ActiveMode: domain.ProtocolVLESSTCPRealityVision,
			DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady,
			Version: 1, UpdatedAt: fixture.now(),
		}
	}
}

func mixedPolicyGroup(id string, vlessPort, mixedPort, slot int) domain.ProxyGroup {
	now := newFixture().now()
	return domain.ProxyGroup{
		ID: id, ResourceName: id, CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter,
		Status: domain.ProxyGroupReady, AimiliSlot: slot, PublicPort: vlessPort, MixedPort: mixedPort,
		ExitIP: "203.0.113.7", ConfigFingerprint: "old-" + id, PublicInboundID: int64(slot*2 + 1), MixedInboundID: int64(slot*2 + 2),
		RealityPublicKey: "pk", RealityShortID: "sid", RealityServerName: "proxy.example.test",
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}
