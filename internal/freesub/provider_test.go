package freesub

import (
	"errors"
	"testing"
)

func TestSameCountryNeverFallsBackToAnotherCountry(t *testing.T) {
	feed := Feed{SchemaVersion: 1, GeneratedAt: "2026-09-16T00:00:00Z", Candidates: []Candidate{
		{CandidateID: "fs-in-1", Protocol: "vless", Country: "IN", RiskScore: 20, LatencyMS: 80, Config: map[string]any{"type": "vless"}},
		{CandidateID: "fs-tr-1", Protocol: "vless", Country: "TR", RiskScore: 1, LatencyMS: 1, Config: map[string]any{"type": "vless"}},
	}}
	if _, err := feed.SameCountry("PH", ""); !errors.Is(err, ErrNoSameCountryCandidate) {
		t.Fatalf("same-country selection error = %v", err)
	}
}

func TestSameCountrySelectionIsDeterministic(t *testing.T) {
	feed := Feed{SchemaVersion: 1, GeneratedAt: "2026-09-16T00:00:00Z", Candidates: []Candidate{
		{CandidateID: "fs-in-slow", Protocol: "trojan", Country: "IN", RiskScore: 10, LatencyMS: 100, Config: map[string]any{"type": "trojan"}},
		{CandidateID: "fs-in-fast", Protocol: "vless", Country: "IN", RiskScore: 10, LatencyMS: 20, Config: map[string]any{"type": "vless"}},
	}}
	candidate, err := feed.SameCountry("IN", "fs-in-old")
	if err != nil || candidate.CandidateID != "fs-in-fast" {
		t.Fatalf("selected candidate = %#v, err=%v", candidate, err)
	}
}
