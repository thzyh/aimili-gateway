package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
)

func TestOverviewRequiresAuthentication(t *testing.T) {
	environment := newOverviewTestEnvironment(t)
	assertResponseStatus(t, environment.request(t, http.MethodGet, "/api/v1/overview", nil, "", ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.request(t, http.MethodGet, "/api/v1/navigation", nil, "", ""), http.StatusUnauthorized)
}

func TestOverviewReturnsIndependentServiceHealthWithoutExpertURL(t *testing.T) {
	environment := newOverviewTestEnvironment(t)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/overview", nil, "", "")
	if response.StatusCode != http.StatusOK {
		assertResponseStatus(t, response, http.StatusOK)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "expert-fixture") {
		t.Fatal("overview exposed expert mode URL")
	}
	var payload struct {
		Gateway struct {
			Health adapters.Health `json:"health"`
		} `json:"gateway"`
		Services            []adapters.ProbeResult `json:"services"`
		ExpertModeAvailable bool                   `json:"expertModeAvailable"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Gateway.Health != adapters.HealthHealthy || !payload.ExpertModeAvailable {
		t.Fatal("gateway overview state is incomplete")
	}
	if len(payload.Services) != 2 {
		t.Fatalf("service count = %d", len(payload.Services))
	}
	if payload.Services[0].Service != "aimili-vpn" || payload.Services[0].Health != adapters.HealthHealthy {
		t.Fatal("AimiliVPN health missing")
	}
	if payload.Services[1].Service != "3x-ui" || payload.Services[1].Health != adapters.HealthUnavailable {
		t.Fatal("3x-ui degradation was not preserved")
	}
}

func TestNavigationReturnsExpertURLOnlyToAuthenticatedSession(t *testing.T) {
	environment := newOverviewTestEnvironment(t)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/navigation", nil, "", "")
	if response.StatusCode != http.StatusOK {
		assertResponseStatus(t, response, http.StatusOK)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("navigation response is cacheable")
	}
	var payload struct {
		ExpertModeURL string `json:"expertModeUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.ExpertModeURL != "/expert-fixture/" {
		t.Fatal("expert mode URL mismatch")
	}
}

func newOverviewTestEnvironment(t *testing.T) *authTestEnvironment {
	t.Helper()
	checkedAt := time.Unix(1_700_000_000, 0).UTC()
	return newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) {
		dependencies.AimiliProbe = staticProber{result: adapters.ProbeResult{
			Service:      "aimili-vpn",
			Health:       adapters.HealthHealthy,
			Capabilities: []string{},
			CheckedAt:    checkedAt,
		}}
		dependencies.XUIProbe = staticProber{result: adapters.ProbeResult{
			Service:      "3x-ui",
			Health:       adapters.HealthUnavailable,
			Capabilities: []string{},
			ErrorCode:    "connection_failed",
			CheckedAt:    checkedAt,
		}}
		dependencies.ExpertModeURL = "/expert-fixture/"
	})
}

type staticProber struct {
	result adapters.ProbeResult
}

func (p staticProber) Probe(context.Context) adapters.ProbeResult {
	return p.result
}
