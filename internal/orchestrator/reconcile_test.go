package orchestrator

import (
	"context"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
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
