package orchestrator

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

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

func TestRepairManagedPersistsRealityMaterialReturnedByXUI(t *testing.T) {
	fixture := newFixture()
	fixture.store.groups = map[string]domain.ProxyGroup{
		"agw-jp-dc-a": mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1),
	}
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
		group.VLESSInboundID != 51 || group.MixedInboundID != 52 || group.ResourceName != "agw-jp-dc-previous" {
		t.Fatalf("repaired Reality material was not persisted: %#v", group)
	}
}

func mixedPolicyGroups() map[string]domain.ProxyGroup {
	return map[string]domain.ProxyGroup{
		"agw-us-res-b": mixedPolicyGroup("agw-us-res-b", 20002, 30002, 2),
		"agw-jp-dc-a":  mixedPolicyGroup("agw-jp-dc-a", 20001, 30001, 1),
	}
}

func mixedPolicyGroup(id string, vlessPort, mixedPort, slot int) domain.ProxyGroup {
	now := newFixture().now()
	return domain.ProxyGroup{
		ID: id, ResourceName: id, CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter,
		Status: domain.ProxyGroupReady, AimiliSlot: slot, VLESSPort: vlessPort, MixedPort: mixedPort,
		ExitIP: "203.0.113.7", ConfigFingerprint: "old-" + id, VLESSInboundID: int64(slot*2 + 1), MixedInboundID: int64(slot*2 + 2),
		RealityPublicKey: "pk", RealityShortID: "sid", RealityServerName: "proxy.example.test",
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}
