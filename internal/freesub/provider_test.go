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

func TestInitialCandidatesRequireSameCountryReplacementAndAreBounded(t *testing.T) {
	feed := Feed{Candidates: []Candidate{
		{CandidateID: "single", Country: "AE", RiskScore: 0, LatencyMS: 1},
		{CandidateID: "us-slow", Country: "US", RiskScore: 10, LatencyMS: 80},
		{CandidateID: "us-fast", Country: "US", RiskScore: 10, LatencyMS: 20},
		{CandidateID: "jp-one", Country: "JP", RiskScore: 20, LatencyMS: 10},
		{CandidateID: "jp-two", Country: "JP", RiskScore: 20, LatencyMS: 20},
	}}
	got := feed.InitialCandidates(2)
	if len(got) != 2 || got[0].CandidateID != "us-fast" || got[1].CandidateID != "us-slow" {
		t.Fatalf("initial candidates = %#v", got)
	}
}

func TestInitialCandidatesReserveSpaceForEachProtocol(t *testing.T) {
	feed := Feed{Candidates: []Candidate{
		{CandidateID: "us-vless-fast", Country: "US", Protocol: "vless", RiskScore: 1, LatencyMS: 10},
		{CandidateID: "us-vless-two", Country: "US", Protocol: "vless", RiskScore: 1, LatencyMS: 20},
		{CandidateID: "us-vless-three", Country: "US", Protocol: "vless", RiskScore: 1, LatencyMS: 30},
		{CandidateID: "us-ss", Country: "US", Protocol: "shadowsocks", RiskScore: 10, LatencyMS: 5},
	}}
	got := feed.InitialCandidates(3)
	if len(got) != 3 || got[0].CandidateID != "us-vless-fast" || got[1].CandidateID != "us-ss" || got[2].CandidateID != "us-vless-two" {
		t.Fatalf("initial candidates = %#v", got)
	}
}
