package freesub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

var (
	ErrInvalidFeed            = errors.New("invalid freesub candidate feed")
	ErrNoSameCountryCandidate = errors.New("no same-country freesub candidate")
)

var supportedProtocols = map[string]bool{
	"vless": true, "vmess": true, "trojan": true, "shadowsocks": true,
}

type Candidate struct {
	CandidateID     string         `json:"candidate_id"`
	Protocol        string         `json:"protocol"`
	Country         string         `json:"country"`
	ExitIP          string         `json:"exit_ip"`
	NetworkType     string         `json:"network_type"`
	ASN             any            `json:"asn"`
	ISP             string         `json:"isp"`
	UpstreamSources []string       `json:"upstream_sources"`
	LatencyMS       int            `json:"latency_ms"`
	SpeedBPS        int            `json:"speed_bps"`
	RiskScore       int            `json:"risk_score"`
	TestedAt        string         `json:"tested_at"`
	Config          map[string]any `json:"config"`
}

type Feed struct {
	SchemaVersion    int         `json:"schema_version"`
	GeneratedAt      string      `json:"generated_at"`
	SourceRepository string      `json:"source_repository"`
	Candidates       []Candidate `json:"candidates"`
}

func Load(path string) (Feed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Feed{}, fmt.Errorf("read freesub feed: %w", err)
	}
	var feed Feed
	if err := json.Unmarshal(data, &feed); err != nil {
		return Feed{}, fmt.Errorf("decode freesub feed: %w", err)
	}
	if err := feed.Validate(); err != nil {
		return Feed{}, err
	}
	return feed, nil
}

func (f Feed) Validate() error {
	if f.SchemaVersion != 1 || strings.TrimSpace(f.GeneratedAt) == "" {
		return ErrInvalidFeed
	}
	seen := make(map[string]struct{}, len(f.Candidates))
	for _, candidate := range f.Candidates {
		if err := candidate.Validate(); err != nil {
			return err
		}
		if _, exists := seen[candidate.CandidateID]; exists {
			return fmt.Errorf("duplicate freesub candidate %q", candidate.CandidateID)
		}
		seen[candidate.CandidateID] = struct{}{}
	}
	return nil
}

func (c Candidate) Validate() error {
	if strings.TrimSpace(c.CandidateID) == "" || len(c.CandidateID) > 256 ||
		len(c.Country) != 2 || c.Country != strings.ToUpper(c.Country) ||
		!supportedProtocols[strings.ToLower(c.Protocol)] || c.Config == nil || c.RiskScore < 0 || c.RiskScore > 100 {
		return ErrInvalidFeed
	}
	return nil
}

func (f Feed) SameCountry(country, excludedID string) (Candidate, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	var candidates []Candidate
	for _, candidate := range f.Candidates {
		if candidate.Country == country && candidate.CandidateID != excludedID {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return Candidate{}, ErrNoSameCountryCandidate
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].RiskScore != candidates[j].RiskScore {
			return candidates[i].RiskScore < candidates[j].RiskScore
		}
		if candidates[i].LatencyMS != candidates[j].LatencyMS {
			return candidates[i].LatencyMS < candidates[j].LatencyMS
		}
		return candidates[i].CandidateID < candidates[j].CandidateID
	})
	return candidates[0], nil
}

// InitialCandidates returns a bounded, deterministic list whose countries
// each have at least two candidates. This preserves a same-country option for
// the one permitted automatic replacement without scanning the full feed.
func (f Feed) InitialCandidates(limit int) []Candidate {
	if limit < 1 {
		return nil
	}
	counts := make(map[string]int)
	for _, candidate := range f.Candidates {
		counts[candidate.Country]++
	}
	result := make([]Candidate, 0, limit)
	for _, candidate := range f.Candidates {
		if counts[candidate.Country] >= 2 {
			result = append(result, candidate)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].RiskScore != result[j].RiskScore {
			return result[i].RiskScore < result[j].RiskScore
		}
		if result[i].LatencyMS != result[j].LatencyMS {
			return result[i].LatencyMS < result[j].LatencyMS
		}
		if result[i].SpeedBPS != result[j].SpeedBPS {
			return result[i].SpeedBPS > result[j].SpeedBPS
		}
		return result[i].CandidateID < result[j].CandidateID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}
