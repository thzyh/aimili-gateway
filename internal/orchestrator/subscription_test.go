package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

func TestSubscriptionAliasesBuildsFourStableLogicalNames(t *testing.T) {
	main := store.MainEgress{ResourceName: "agw-main", Enabled: true, PublicInboundID: 1, CountryName: "日本"}
	groups := []domain.ProxyGroup{
		{ResourceName: "agw-slot-two", Status: domain.ProxyGroupReady, AimiliSlot: 1, PublicInboundID: 3, CountryName: "美国"},
		{ResourceName: "agw-slot-one", Status: domain.ProxyGroupReady, AimiliSlot: 0, PublicInboundID: 2, CountryName: "日本"},
		{ResourceName: "agw-slot-three", Status: domain.ProxyGroupReady, AimiliSlot: 2, PublicInboundID: 4, CountryName: "韩国"},
	}

	got, err := subscriptionAliases(main, groups)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{1: "主连接_日本", 2: "出口位 1_日本", 3: "出口位 2_美国", 4: "出口位 3_韩国"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %#v, want %#v", got, want)
	}
}

func TestSubscriptionAliasesBuildsAnyContiguousNumberOfExitSlots(t *testing.T) {
	main := store.MainEgress{ResourceName: "agw-main", Enabled: true, PublicInboundID: 1, CountryName: "日本"}
	groups := make([]domain.ProxyGroup, 5)
	for slot := range groups {
		groups[slot] = domain.ProxyGroup{
			ResourceName: fmt.Sprintf("agw-slot-%d", slot), Status: domain.ProxyGroupReady,
			AimiliSlot: slot, PublicInboundID: int64(slot + 2), CountryName: "日本",
		}
	}
	aliases, err := subscriptionAliases(main, groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 6 || aliases[6] != "出口位 5_日本" {
		t.Fatalf("aliases = %#v", aliases)
	}
}

func TestSubscriptionAliasesRejectsDuplicateLogicalSlot(t *testing.T) {
	_, err := subscriptionAliases(
		store.MainEgress{ResourceName: "agw-main", Enabled: true, PublicInboundID: 1, CountryName: "日本"},
		[]domain.ProxyGroup{
			{ResourceName: "agw-one", Status: domain.ProxyGroupReady, AimiliSlot: 0, PublicInboundID: 2, CountryName: "日本"},
			{ResourceName: "agw-two", Status: domain.ProxyGroupReady, AimiliSlot: 0, PublicInboundID: 3, CountryName: "美国"},
			{ResourceName: "agw-three", Status: domain.ProxyGroupReady, AimiliSlot: 2, PublicInboundID: 4, CountryName: "韩国"},
		},
	)
	if codeOf(err) != "invalid_request" {
		t.Fatalf("error = %v", err)
	}
}

func TestSubscriptionAliasesRejectsBlankCountry(t *testing.T) {
	_, err := subscriptionAliases(
		store.MainEgress{ResourceName: "agw-main", Enabled: true, PublicInboundID: 1, CountryName: "日本"},
		[]domain.ProxyGroup{
			{ResourceName: "agw-one", Status: domain.ProxyGroupReady, AimiliSlot: 0, PublicInboundID: 2, CountryName: "日本"},
			{ResourceName: "agw-two", Status: domain.ProxyGroupReady, AimiliSlot: 1, PublicInboundID: 3, CountryName: ""},
			{ResourceName: "agw-three", Status: domain.ProxyGroupReady, AimiliSlot: 2, PublicInboundID: 4, CountryName: "韩国"},
		},
	)
	if codeOf(err) != "invalid_request" {
		t.Fatalf("error = %v", err)
	}
}

func TestSubscriptionPassesFourVerifiedAliasesToTheExclusiveClient(t *testing.T) {
	fixture := newFixture()
	fixture.store.subscription = store.GatewaySubscription{ResourceName: "aimili-gateway-subscription", ClientID: 42, SubscriptionID: "stable-sub", UpdatedAt: fixture.now()}
	fixture.store.mainEgress = store.MainEgress{ResourceName: "agw-main", Enabled: true, PublicInboundID: 1, CountryName: "日本", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "main", ExitIP: "203.0.113.1", PublicPort: 8443, MixedInboundID: 98, MixedPort: 31000, UpdatedAt: fixture.now()}
	countries := []struct {
		code string
		name string
	}{
		{code: "JP", name: "日本"}, {code: "US", name: "美国"}, {code: "KR", name: "韩国"},
	}
	inbounds := []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	publicInboundIDs := []int64{12, 5, 9}
	for slot, country := range countries {
		group, _ := domain.NewProxyGroupIdentity(country.code, domain.ProxyTypeDatacenter, fmt.Sprintf("node-%d", slot))
		group.Status, group.AimiliSlot, group.PublicInboundID, group.MixedInboundID = domain.ProxyGroupReady, slot, publicInboundIDs[slot], int64(slot+20)
		group.CountryName, group.PublicPort, group.MixedPort, group.ExitIP = country.name, 20000+slot, 30000+slot, fmt.Sprintf("203.0.113.%d", slot+2)
		fixture.store.groups[group.ID] = group
		inbounds = append(inbounds, xui.Inbound{ID: group.PublicInboundID, Tag: group.ResourceName + "-vless", Remark: "Aimili Gateway " + group.ResourceName + " VLESS", Protocol: "vless", Port: group.PublicPort})
	}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: inbounds}

	_, err := fixture.orchestratorWithMax(t, 3).Subscription(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{1: "主连接_日本", 12: "出口位 1_日本", 5: "出口位 2_美国", 9: "出口位 3_韩国"}
	if !reflect.DeepEqual(fixture.xui.subscriptionDesired.Aliases, want) {
		t.Fatalf("desired aliases = %#v, want %#v", fixture.xui.subscriptionDesired.Aliases, want)
	}
	if fixture.xui.subscriptionDesired.SubscriptionID != "stable-sub" {
		t.Fatalf("persisted subscription ID was not supplied for recovery")
	}
	if got := fixture.xui.subscriptionDesired.InboundIDs; !reflect.DeepEqual(got, []int64{1, 12, 5, 9}) {
		t.Fatalf("subscription inbound order = %#v", got)
	}
}

func TestReplaceCandidateUpdatesOnlyTheTargetSubscriptionAlias(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{ResourceName: "agw-main", Enabled: true, PublicInboundID: 1, CountryName: "日本", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "main", ExitIP: "203.0.113.1", PublicPort: 8443, MixedInboundID: 98, MixedPort: 31000, UpdatedAt: fixture.now()}
	countries := []struct {
		code string
		name string
		node string
	}{
		{code: "JP", name: "日本", node: "old-node"}, {code: "US", name: "美国", node: "us-node"}, {code: "KR", name: "韩国", node: "kr-node"},
	}
	inbounds := []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	var target domain.ProxyGroup
	fixture.aimili.createdSlots = make(map[int]aimili.Slot)
	for slot, country := range countries {
		group, _ := domain.NewProxyGroupIdentity(country.code, domain.ProxyTypeDatacenter, country.node)
		group.Status, group.AimiliSlot, group.PublicInboundID, group.MixedInboundID = domain.ProxyGroupReady, slot, int64(slot+2), int64(slot+20)
		group.CountryName, group.PublicPort, group.MixedPort, group.ExitIP = country.name, 20000+slot, 30000+slot, fmt.Sprintf("203.0.113.%d", slot+2)
		group.CandidateID, group.RealityPublicKey, group.RealityShortID, group.RealityServerName = country.node, "key", "short", "proxy.example.test"
		fixture.store.groups[group.ID] = group
		fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
		fixture.aimili.createdSlots[slot] = aimili.Slot{Number: slot, NodeID: country.node, Country: country.code, CountryName: country.name, ProxyType: "datacenter", ExitIP: group.ExitIP, Port: 17930 + slot, Status: "up", EgressOK: true}
		inbounds = append(inbounds, xui.Inbound{ID: group.PublicInboundID, Tag: group.ResourceName + "-vless", Remark: "Aimili Gateway " + group.ResourceName + " VLESS", Protocol: "vless", Port: group.PublicPort})
		if slot == 0 {
			target = group
		}
	}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: inbounds}
	fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "KR", CountryName: "韩国", ProxyType: "datacenter", ProbeStatus: "available", IP: "198.51.100.99"}}
	fixture.aimili.assignedSlot = aimili.Slot{Number: 0, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", ExitIP: "203.0.113.99", Port: 17930, Status: "up", EgressOK: true}

	_, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", target.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{1: "主连接_日本", 2: "出口位 1_韩国", 3: "出口位 2_美国", 4: "出口位 3_韩国"}
	if !reflect.DeepEqual(fixture.xui.subscriptionDesired.Aliases, want) {
		t.Fatalf("desired aliases = %#v, want %#v", fixture.xui.subscriptionDesired.Aliases, want)
	}
}

func TestReplaceCandidateAliasFailuresRestoreOldState(t *testing.T) {
	tests := []struct {
		name       string
		errors     []error
		drifts     []bool
		wantRepair bool
	}{
		{name: "alias write failure", errors: []error{errors.New("alias write failed"), nil}},
		{name: "subscription fetch failure", errors: []error{errors.New("subscription fetch failed"), nil}},
		{name: "alias read mismatch", drifts: []bool{true, false}},
		{name: "alias rollback failure", errors: []error{errors.New("alias write failed"), errors.New("alias rollback failed")}, wantRepair: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, target := aliasReplacementFixture(t)
			fixture.xui.ensureSubscriptionErrors = test.errors
			fixture.xui.ensureSubscriptionAliasDrifts = test.drifts
			_, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", target.ID)
			if test.wantRepair {
				if codeOf(err) != "repair_required" || fixture.store.groups[target.ID].Status != domain.ProxyGroupRepairRequired || fixture.aimili.createdSlots[0].NodeID != "old-node" {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("alias failure committed replacement")
			}
			stored := fixture.store.groups[target.ID]
			if stored.CandidateID != "old-node" || stored.CountryName != "日本" || stored.Status != domain.ProxyGroupReady {
				t.Fatalf("old group not restored: %#v", stored)
			}
			if slot := fixture.aimili.createdSlots[0]; slot.NodeID != "old-node" || slot.Country != "JP" || !slot.EgressOK || slot.Status != "up" {
				t.Fatalf("AimiliVPN target slot not restored: %#v", slot)
			}
			if fixture.aimili.createdSlots[1].NodeID != "us-node" || fixture.aimili.createdSlots[2].NodeID != "kr-node" {
				t.Fatalf("non-target slots changed: %#v", fixture.aimili.createdSlots)
			}
			want := map[int64]string{1: "主连接_日本", 2: "出口位 1_日本", 3: "出口位 2_美国", 4: "出口位 3_韩国"}
			if !reflect.DeepEqual(fixture.xui.subscriptionDesired.Aliases, want) || fixture.xui.ensureSubscriptionCalls != 2 {
				t.Fatalf("aliases=%#v writes=%d", fixture.xui.subscriptionDesired.Aliases, fixture.xui.ensureSubscriptionCalls)
			}
		})
	}
}

func aliasReplacementFixture(t *testing.T) (*fixture, domain.ProxyGroup) {
	t.Helper()
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{ResourceName: "agw-main", Enabled: true, PublicInboundID: 1, CountryName: "日本", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "main", ExitIP: "203.0.113.1", PublicPort: 8443, MixedInboundID: 98, MixedPort: 31000, UpdatedAt: fixture.now()}
	values := []struct{ code, name, node string }{{"JP", "日本", "old-node"}, {"US", "美国", "us-node"}, {"KR", "韩国", "kr-node"}}
	fixture.aimili.createdSlots = map[int]aimili.Slot{}
	inbounds := []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	var target domain.ProxyGroup
	for slot, value := range values {
		group, _ := domain.NewProxyGroupIdentity(value.code, domain.ProxyTypeDatacenter, value.node)
		group.Status, group.AimiliSlot, group.PublicInboundID, group.MixedInboundID = domain.ProxyGroupReady, slot, int64(slot+2), int64(slot+20)
		group.CountryName, group.PublicPort, group.MixedPort, group.ExitIP, group.CandidateID = value.name, 20000+slot, 30000+slot, fmt.Sprintf("203.0.113.%d", slot+2), value.node
		group.RealityPublicKey, group.RealityShortID, group.RealityServerName = "key", "short", "proxy.example.test"
		fixture.store.groups[group.ID] = group
		fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
		fixture.aimili.createdSlots[slot] = aimili.Slot{Number: slot, NodeID: value.node, Country: value.code, CountryName: value.name, ProxyType: "datacenter", ExitIP: group.ExitIP, Status: "up", EgressOK: true}
		inbounds = append(inbounds, xui.Inbound{ID: group.PublicInboundID, Tag: group.ResourceName + "-vless", Remark: "Aimili Gateway " + group.ResourceName + " VLESS", Protocol: "vless", Port: group.PublicPort})
		if slot == 0 {
			target = group
		}
	}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: inbounds}
	fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "KR", CountryName: "韩国", ProxyType: "datacenter", ProbeStatus: "available", IP: "198.51.100.99"}}
	fixture.aimili.assignedSlots = []aimili.Slot{
		{Number: 0, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", ExitIP: "203.0.113.99", Status: "up", EgressOK: true},
		fixture.aimili.createdSlots[0],
	}
	return fixture, target
}

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

func TestSubscriptionURLKeepsConfiguredPublicOriginPort(t *testing.T) {
	fixture := newFixture()
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.config.PublicOrigin = "https://192.168.88.4:8080"
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "jp-ready")
	group.Status, group.PublicInboundID, group.AimiliSlot = domain.ProxyGroupReady, 11, 0
	group.PublicPort, group.MixedPort = 20000, 30000
	fixture.store.groups[group.ID] = group
	fixture.xui.snapshot = xui.Snapshot{Inbounds: []xui.Inbound{
		{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443},
		{ID: 11, Tag: group.ResourceName + "-vless", Protocol: "vless", Port: 20000},
	}}
	result, err := orchestrator.Subscription(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.URL, "https://192.168.88.4:8080/") {
		t.Fatalf("subscription URL = %q", result.URL)
	}
}

func TestSubscriptionIncludesTheOwnedHysteriaMainInbound(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{ResourceName: "agw-main", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "main-node", ExitIP: "203.0.113.20", PublicInboundID: 1, MixedInboundID: 98, PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now()}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{EgressID: "agw-main", ActiveMode: domain.ProtocolHysteria2QUICTLS, DesiredMode: domain.ProtocolHysteria2QUICTLS, State: domain.ProtocolReady, Version: 2, UpdatedAt: fixture.now()}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: []xui.Inbound{
		{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "hysteria", Port: 8443},
		{ID: 98, Tag: "aimili-main-mixed", Remark: "Aimili Gateway main mixed", Protocol: "mixed", Port: 31000},
	}}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: 1, Mode: domain.ProtocolHysteria2QUICTLS, Auth: "test-auth"}}

	result, err := fixture.orchestratorWithMax(t, 3).Subscription(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.InboundCount != 1 || len(result.PublicProfiles) != 1 || result.PublicProfiles[0].Mode != domain.ProtocolHysteria2QUICTLS || len(fixture.xui.subscriptionDesired.InboundIDs) != 1 || fixture.xui.subscriptionDesired.InboundIDs[0] != 1 {
		t.Fatalf("result=%#v desired=%#v", result, fixture.xui.subscriptionDesired)
	}
}

func TestConnectionsUseNeutralPublicURIForHysteria2AndKeepSOCKS5H(t *testing.T) {
	fixture := newFixture()
	group, _ := domain.NewProxyGroupIdentity("US", domain.ProxyTypeDatacenter, "hy2-ready")
	group.Status = domain.ProxyGroupReady
	group.PublicInboundID = 21
	group.MixedInboundID = 22
	group.AimiliSlot = 1
	group.PublicPort = 20001
	group.MixedPort = 30001
	group.ExitIP = "203.0.113.8"
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolHysteria2QUICTLS, DesiredMode: domain.ProtocolHysteria2QUICTLS, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: 21, Mode: domain.ProtocolHysteria2QUICTLS, Auth: "test-auth", ClientID: "must-not-be-used", ServerName: "tls.example.test"}}

	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.config.PublicHost = "192.0.2.20"
	connections, err := orchestrator.Connections(context.Background(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(connections.PublicURI, "hysteria2://") || connections.VLESSURI != "" || connections.VLESSError != "protocol_changed" || !strings.HasPrefix(connections.SOCKS5HURI, "socks5h://") {
		t.Fatal("neutral connection response is incomplete")
	}
	if strings.Contains(connections.PublicURI, "must-not-be-used") {
		t.Fatal("Hysteria2 connection reused a VLESS identity")
	}
	parsed, err := url.Parse(connections.PublicURI)
	if err != nil || parsed.Hostname() != "192.0.2.20" || parsed.Query().Get("sni") != "tls.example.test" {
		t.Fatalf("Hysteria2 endpoint and TLS identity were conflated: %q", connections.PublicURI)
	}
	if socks, err := url.Parse(connections.SOCKS5HURI); err != nil || socks.Hostname() != "192.0.2.20" {
		t.Fatalf("SOCKS endpoint does not use the public address: %q", connections.SOCKS5HURI)
	}
}

func TestConnectionsBuildXHTTPPublicURIFromCurrentSubscriptionProfile(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "test-client", PublicKey: "test-public", ShortID: "test-short", ServerName: "proxy.example.test", XHTTPPath: "/opaque-path"}}

	connections, err := fixture.orchestratorWithMax(t, 3).Connections(context.Background(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if connections.PublicURI == "" || connections.PublicURI != connections.VLESSURI || !strings.Contains(connections.PublicURI, "type=xhttp") || !strings.Contains(connections.PublicURI, "path=%2Fopaque-path") || strings.Contains(connections.PublicURI, "xtls-rprx-vision") {
		t.Fatal("XHTTP connection URI does not reflect the current profile")
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
	group.PublicInboundID = 21
	group.ExitIP = "203.0.113.7"
	group.RealityPublicKey = "pk"
	group.RealityShortID = "sid"
	group.RealityServerName = "proxy.example.test"
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.aimili.createdSlots = map[int]aimili.Slot{2: {Number: 2, NodeID: "old-node", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.7", Port: 17932, Status: "up", EgressOK: true}}
	fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "KR", CountryName: "韩国", ProxyType: "datacenter", ProbeStatus: "available", IP: "198.51.100.8", LatencyMS: 45}}
	fixture.aimili.assignedSlot = aimili.Slot{Number: 2, NodeID: "new-node", Country: "KR", CountryName: "韩国", ProxyType: "datacenter", ExitIP: "203.0.113.8", CheckedAt: 1_700_000_010.5, Port: 17932, Status: "up", EgressOK: true}
	updated, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CandidateID != "new-node" || updated.AimiliSlot != 2 || updated.PublicPort != 20000 || updated.MixedPort != 30000 || updated.ExitIP != "203.0.113.8" || updated.ExitIPCheckedAt != 1_700_000_010.5 {
		t.Fatalf("updated=%#v", updated)
	}
}

func TestReplaceCandidateAllowsManualRecoveryOfFailedSlot(t *testing.T) {
	for _, failedStatus := range []domain.ProxyGroupStatus{domain.ProxyGroupDegraded, domain.ProxyGroupRepairRequired} {
		t.Run(string(failedStatus), func(t *testing.T) {
			fixture := newFixture()
			group, _ := domain.NewProxyGroupIdentity("RU", domain.ProxyTypeResidential, "failed-node")
			group.Status = failedStatus
			group.AimiliSlot = 0
			group.PublicPort = 20000
			group.MixedPort = 30000
			group.PublicInboundID = 21
			group.ExitIP = "203.0.113.7"
			group.RealityPublicKey = "pk"
			group.RealityShortID = "sid"
			group.RealityServerName = "proxy.example.test"
			group.LastErrorCode = "manual_replacement_required"
			group.CreatedAt = fixture.now()
			group.UpdatedAt = fixture.now()
			fixture.store.groups[group.ID] = group
			fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
			fixture.aimili.createdSlots = map[int]aimili.Slot{0: {Number: 0, NodeID: "failed-node", Country: "RU", CountryName: "俄罗斯", ProxyType: "residential", Port: 17930, Status: "down", EgressOK: false}}
			fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "US", CountryName: "美国", ProxyType: "residential", ProbeStatus: "available", IP: "198.51.100.8", LatencyMS: 45}}
			fixture.aimili.assignedSlot = aimili.Slot{Number: 0, NodeID: "new-node", Country: "US", CountryName: "美国", ProxyType: "residential", ExitIP: "203.0.113.8", CheckedAt: 1_700_000_010.5, Port: 17930, Status: "up", EgressOK: true}

			updated, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", group.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Status != domain.ProxyGroupReady || updated.CandidateID != "new-node" || updated.AimiliSlot != 0 || updated.PublicPort != 20000 || updated.MixedPort != 30000 || updated.LastErrorCode != "" {
				t.Fatalf("updated=%#v", updated)
			}
		})
	}
}

func TestReplaceCandidateReloadsCandidatesOnlyAfterPersistedRejection(t *testing.T) {
	tests := []struct {
		name      string
		assignErr error
		checkErr  error
		wantReads int
	}{
		{name: "persisted candidate rejection", assignErr: &aimili.AdapterError{Code: "candidate_dial_failed", CandidateRejected: true}, wantReads: 2},
		{name: "persisted egress rejection", checkErr: &aimili.AdapterError{Code: "candidate_egress_failed", CandidateRejected: true}, wantReads: 2},
		{name: "outer assignment failure", assignErr: &aimili.AdapterError{Code: "upstream_rejected"}, wantReads: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture()
			group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "old-node")
			group.Status = domain.ProxyGroupReady
			group.AimiliSlot = 2
			group.PublicPort = 20000
			group.MixedPort = 30000
			group.ExitIP = "203.0.113.7"
			group.CreatedAt = fixture.now()
			group.UpdatedAt = fixture.now()
			fixture.store.groups[group.ID] = group
			fixture.aimili.createdSlots = map[int]aimili.Slot{2: {Number: 2, NodeID: "old-node", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.7", Port: 17932, Status: "up", EgressOK: true}}
			fixture.aimili.candidates = []aimili.Candidate{{ID: "new-node", CountryCode: "KR", CountryName: "韩国", IP: "198.51.100.8", ProxyType: "datacenter", ProbeStatus: "available"}}
			fixture.aimili.assignErrors = []error{test.assignErr, nil}
			fixture.aimili.checkErrors = []error{test.checkErr}
			fixture.aimili.assignedSlots = []aimili.Slot{{}, {Number: 2, NodeID: "old-node", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.7", Port: 17932, Status: "up", EgressOK: true}}

			_, _ = fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-node", group.ID)
			if fixture.aimili.candidateReads != test.wantReads {
				t.Fatalf("candidate reads=%d, want=%d", fixture.aimili.candidateReads, test.wantReads)
			}
		})
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
	group.PublicInboundID = 21
	group.ExitIP = "203.0.113.7"
	group.RealityPublicKey = "pk"
	group.RealityShortID = "sid"
	group.RealityServerName = "proxy.example.test"
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
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

func TestReplaceCandidateRejectsMissingTarget(t *testing.T) {
	fixture := newFixture()
	o := fixture.orchestratorWithMax(t, 3)
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
	mode, ok := fixture.store.protocolModes["agw-main"]
	if !ok || mode.ActiveMode != domain.ProtocolVLESSTCPRealityVision || mode.DesiredMode != domain.ProtocolVLESSTCPRealityVision || mode.State != domain.ProtocolReady {
		t.Fatalf("fresh main protocol state was not initialized: %#v", mode)
	}
}

func TestCheckMainRefreshesDynamicSubscriptionAfterMainIdentityDrift(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{ResourceName: "agw-main", CandidateID: "old-main", CountryCode: "US", CountryName: "美国", ProxyType: domain.ProxyTypeResidential, ExitIP: "203.0.113.10", PublicInboundID: 1, MixedInboundID: 98, PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now()}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.aimili.mainStatus = aimili.MainStatus{CandidateID: "new-main", Country: "CA", CountryName: "加拿大", ProxyType: "datacenter", ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}}
	for slot, country := range []string{"日本", "韩国", "美国"} {
		group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, fmt.Sprintf("node-%d", slot))
		group.Status, group.AimiliSlot, group.PublicInboundID, group.MixedInboundID = domain.ProxyGroupReady, slot, int64(slot+2), int64(slot+20)
		group.CountryName, group.PublicPort, group.MixedPort = country, 20000+slot, 30000+slot
		fixture.store.groups[group.ID] = group
		fixture.xui.snapshot.Inbounds = append(fixture.xui.snapshot.Inbounds, xui.Inbound{ID: group.PublicInboundID, Tag: group.ResourceName + "-vless", Remark: "Aimili Gateway " + group.ResourceName + " VLESS", Protocol: "vless", Port: group.PublicPort})
	}

	main, err := fixture.orchestratorWithMax(t, 3).CheckMain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if main.CountryName != "加拿大" || main.ExitIP != "203.0.113.20" || fixture.xui.ensureSubscriptionCalls != 2 {
		t.Fatalf("main=%#v subscription_writes=%d", main, fixture.xui.ensureSubscriptionCalls)
	}
	if fixture.xui.subscriptionDesired.Aliases[1] != "主连接_加拿大" || fixture.xui.subscriptionDesired.Aliases[2] != "出口位 1_日本" {
		t.Fatalf("aliases=%#v", fixture.xui.subscriptionDesired.Aliases)
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

func TestCheckMainValidatesStoredXHTTPProfileWithoutRecreatingLegacyTCP(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainStatus = aimili.MainStatus{CandidateID: "main-node", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true}
	fixture.store.mainEgress = store.MainEgress{ResourceName: "agw-main", CandidateID: "main-node", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter, ExitIP: "203.0.113.20", PublicInboundID: 1, MixedInboundID: 98, PublicPort: 8443, MixedPort: 30003, Enabled: true, UpdatedAt: fixture.now()}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 2, UpdatedAt: fixture.now()}
	fixture.xui.snapshot = xui.Snapshot{Inbounds: []xui.Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: 1, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "test-client", PublicKey: "test-public", ShortID: "test-short", ServerName: "proxy.example.test", XHTTPPath: "/main-test"}}

	main, err := fixture.orchestratorWithMax(t, 3).CheckMain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fixture.xui.ensureLegacyMainCalls != 0 || len(fixture.validator.publicTargets) != 1 || fixture.validator.publicTargets[0].Mode != domain.ProtocolVLESSXHTTPReality || main.PublicInboundID != 1 || main.ExitIP != "203.0.113.20" {
		t.Fatalf("main=%#v legacyCalls=%d publicTargets=%#v", main, fixture.xui.ensureLegacyMainCalls, fixture.validator.publicTargets)
	}
}
