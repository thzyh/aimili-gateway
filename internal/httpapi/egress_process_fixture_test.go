package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
)

type processFixtureManager struct {
	fakeProxyManager
	aimiliURL string
	xuiURL    string
}

func fixtureRequest(ctx context.Context, method, url string, input, output any) (int, error) {
	var body *bytes.Reader
	if input == nil {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}

func (m *processFixtureManager) SwitchProtocolModeExpected(ctx context.Context, egressID string, target, _ domain.ProtocolMode) (domain.EgressProtocolMode, error) {
	var main struct {
		CandidateID string `json:"candidateId"`
		Country     string `json:"country"`
	}
	status, err := fixtureRequest(ctx, http.MethodGet, m.aimiliURL+"/control/v1/main", nil, &main)
	if err != nil || status != http.StatusOK || main.CandidateID == "" {
		return domain.EgressProtocolMode{}, &orchestrator.Error{Code: "egress_unavailable"}
	}
	for index := range m.groups {
		if m.groups[index].ID == egressID {
			m.groups[index].CandidateID = main.CandidateID
			m.groups[index].CountryName = main.Country
		}
	}
	return domain.EgressProtocolMode{EgressID: egressID, ActiveMode: target, DesiredMode: target, State: domain.ProtocolReady, Version: 2, UpdatedAt: time.Unix(1_700_000_000, 0).UTC()}, nil
}

func (m *processFixtureManager) ReplaceCandidate(ctx context.Context, candidateID, targetID string) (domain.ProxyGroup, error) {
	var slot struct {
		CandidateID string `json:"candidateId"`
		Country     string `json:"country"`
		PublicPort  int    `json:"publicPort"`
		ErrorCode   string `json:"errorCode"`
	}
	status, err := fixtureRequest(ctx, http.MethodPost, m.aimiliURL+"/control/v1/slots/1/assign", map[string]string{"candidateId": candidateID}, &slot)
	if err != nil {
		return domain.ProxyGroup{}, &orchestrator.Error{Code: "operation_failed"}
	}
	if status != http.StatusOK {
		if slot.ErrorCode == "" {
			slot.ErrorCode = "operation_failed"
		}
		return domain.ProxyGroup{}, &orchestrator.Error{Code: slot.ErrorCode}
	}
	alias := "出口位 1_" + slot.Country
	var aliases map[string]any
	status, err = fixtureRequest(ctx, http.MethodPost, m.xuiURL+"/panel/api/clients/gateway/inboundAliases", map[string]any{"aliases": map[string]string{"slot1": alias}}, &aliases)
	if err != nil || status != http.StatusOK {
		return domain.ProxyGroup{}, &orchestrator.Error{Code: "subscription_failed"}
	}
	for index := range m.groups {
		if m.groups[index].ID == targetID {
			m.groups[index].CandidateID = slot.CandidateID
			m.groups[index].CountryCode = "KR"
			m.groups[index].CountryName = slot.Country
			m.groups[index].PublicPort = slot.PublicPort
			return m.groups[index], nil
		}
	}
	return domain.ProxyGroup{}, &orchestrator.Error{Code: "not_found"}
}

func TestEgressProcessGatewayHelper(t *testing.T) {
	if os.Getenv("AIMILI_EGRESS_PROCESS_HELPER") != "1" {
		t.Skip("process helper")
	}
	readyPath := os.Getenv("AIMILI_EGRESS_READY_FILE")
	stopPath := os.Getenv("AIMILI_EGRESS_STOP_FILE")
	if readyPath == "" || stopPath == "" {
		t.Fatal("fixture control paths are required")
	}
	manager := &processFixtureManager{
		aimiliURL: os.Getenv("AIMILI_EGRESS_AIMILI_URL"),
		xuiURL:    os.Getenv("AIMILI_EGRESS_XUI_URL"),
	}
	manager.groups = []domain.ProxyGroup{
		{ID: "agw-main", CandidateID: "stale-main", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter, Status: domain.ProxyGroupReady, PublicPort: 8443, MixedPort: 31000},
		{ID: "agw-slot-1", CandidateID: "candidate-jp", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter, Status: domain.ProxyGroupReady, PublicPort: 20000, MixedPort: 30000},
	}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) {
		dependencies.ProxyManager = manager
	})
	ready, err := json.Marshal(map[string]string{"url": environment.server.URL, "origin": environment.origin})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Clean(readyPath), ready, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(stopPath); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture stop signal timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
