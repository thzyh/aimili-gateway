package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/backendlogin"
	"github.com/thzyh/aimili-gateway/internal/maintenance"
)

func TestBackendLoginRequiresAuthenticatedSameOriginBodylessPOST(t *testing.T) {
	login := &fakeBackendLogin{sessions: map[backendlogin.Target]backendlogin.Session{
		backendlogin.TargetAimiliVPN: {CookieName: "session", Token: []byte("opaque-backend-cookie-value"), Path: "/native-fixture/", Location: "/native-fixture/", ExpiresAt: time.Unix(1_700_000_300, 0).UTC()},
	}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.BackendLogin = login })
	path := "/api/v1/backends/aimilivpn/login"
	assertResponseStatus(t, environment.request(t, http.MethodGet, path, nil, "", ""), http.StatusMethodNotAllowed)
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, nil, environment.origin, ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, nil, "", csrf), http.StatusForbidden)
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, nil, environment.origin, ""), http.StatusForbidden)
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]any{}, environment.origin, csrf), http.StatusBadRequest)

	request := environment.newRequest(t, http.MethodPost, path, nil, environment.origin, csrf)
	client := *environment.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/native-fixture/" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d location=%q cache=%q", response.StatusCode, response.Header.Get("Location"), response.Header.Get("Cache-Control"))
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Path != "/native-fixture/" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie attributes = %#v", cookies)
	}
	if login.calls != 1 {
		t.Fatalf("login calls = %d", login.calls)
	}
}

func TestBackendLoginFailureOffersManualFallbackWithoutSecrets(t *testing.T) {
	login := &fakeBackendLogin{err: &backendlogin.Error{Code: "account_drift"}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.BackendLogin = login })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	response := environment.request(t, http.MethodPost, "/api/v1/backends/3x-ui/login", nil, environment.origin, csrf)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var payload struct {
		Error       string `json:"error"`
		ManualLogin bool   `json:"manualLogin"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "account_drift" || !payload.ManualLogin {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSettingsRoutesExposeOnlyMaintenanceServiceResults(t *testing.T) {
	service := &fakeMaintenance{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Maintenance = service })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	for _, path := range []string{"/api/v1/settings/summary", "/api/v1/settings/aimilivpn", "/api/v1/settings/3x-ui"} {
		response := environment.request(t, http.MethodGet, path, nil, "", "")
		if response.StatusCode != http.StatusOK {
			assertResponseStatus(t, response, http.StatusOK)
		}
		var value map[string]any
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		encoded, _ := json.Marshal(value)
		for _, forbidden := range []string{"password", "cookie", "random-path", "secret"} {
			if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
				t.Fatalf("%s leaked %q: %s", path, forbidden, encoded)
			}
		}
	}
	csrf := environment.session(t).CSRFToken
	for _, path := range []string{"/api/v1/settings/aimilivpn/check", "/api/v1/settings/3x-ui/check", "/api/v1/settings/3x-ui/repair"} {
		assertResponseStatus(t, environment.request(t, http.MethodPost, path, nil, environment.origin, csrf), http.StatusOK)
	}
}

func TestAimiliCountryRefreshRoutesEnforceSessionMutationAndIdempotency(t *testing.T) {
	service := &fakeMaintenance{
		countries:   []aimili.CandidateCountry{{Code: "JP", Name: "日本", CandidateCount: 8, ObservedAt: 1_700_000_000}},
		refresh:     aimili.CountryRefresh{State: "completed", Country: "JP", TestedCount: 5, ValidCount: 4},
		startResult: aimili.CountryRefresh{State: "running", Country: "JP", Phase: "fetching"},
	}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Maintenance = service })
	assertResponseStatus(t, environment.request(t, http.MethodGet, "/api/v1/settings/aimilivpn/countries", nil, "", ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)

	for _, path := range []string{"/api/v1/settings/aimilivpn/countries", "/api/v1/settings/aimilivpn/refresh"} {
		response := environment.request(t, http.MethodGet, path, nil, "", "")
		if response.StatusCode != http.StatusOK {
			assertResponseStatus(t, response, http.StatusOK)
		}
		var payload any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		encoded, _ := json.Marshal(payload)
		for _, forbidden := range []string{"password", "token", "config", "exception"} {
			if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
				t.Fatalf("%s leaked %q: %s", path, forbidden, encoded)
			}
		}
	}

	path := "/api/v1/settings/aimilivpn/refresh"
	csrf := environment.session(t).CSRFToken
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]string{"country": "JP"}, "", csrf), http.StatusForbidden)
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]string{"country": "JP"}, environment.origin, ""), http.StatusForbidden)
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]string{"country": "JP"}, environment.origin, csrf), http.StatusPreconditionRequired)
	assertResponseStatus(t, environment.requestWithHeaders(t, http.MethodPost, path, map[string]any{"country": "JP", "config": "forbidden"}, environment.origin, csrf, map[string]string{"Idempotency-Key": "bad-refresh"}), http.StatusBadRequest)

	response := environment.requestWithHeaders(t, http.MethodPost, path, map[string]string{"country": "jp"}, environment.origin, csrf, map[string]string{"Idempotency-Key": "refresh-jp"})
	assertResponseStatus(t, response, http.StatusAccepted)
	response = environment.requestWithHeaders(t, http.MethodPost, path, map[string]string{"country": "jp"}, environment.origin, csrf, map[string]string{"Idempotency-Key": "refresh-jp"})
	assertResponseStatus(t, response, http.StatusAccepted)
	if len(service.startedCountries) != 1 || service.startedCountries[0] != "JP" {
		t.Fatalf("started countries = %#v", service.startedCountries)
	}
	response = environment.requestWithHeaders(t, http.MethodPost, path, map[string]string{"country": "all"}, environment.origin, csrf, map[string]string{"Idempotency-Key": "refresh-all"})
	assertResponseStatus(t, response, http.StatusAccepted)
	if len(service.startedCountries) != 2 || service.startedCountries[1] != "ALL" {
		t.Fatalf("all-country refresh was not forwarded: %#v", service.startedCountries)
	}

	service.startErr = &maintenance.Error{Code: "maintenance_busy"}
	response = environment.requestWithHeaders(t, http.MethodPost, path, map[string]string{"country": "US"}, environment.origin, csrf, map[string]string{"Idempotency-Key": "refresh-us"})
	assertResponseStatus(t, response, http.StatusConflict)
}

type fakeBackendLogin struct {
	sessions map[backendlogin.Target]backendlogin.Session
	err      error
	calls    int
}

func (fake *fakeBackendLogin) Login(_ context.Context, target backendlogin.Target) (backendlogin.Session, error) {
	fake.calls++
	return fake.sessions[target], fake.err
}

type fakeMaintenance struct {
	countries        []aimili.CandidateCountry
	refresh          aimili.CountryRefresh
	startResult      aimili.CountryRefresh
	startErr         error
	startedCountries []string
}

func (*fakeMaintenance) Summary(context.Context) (maintenance.Summary, error) {
	return maintenance.Summary{CandidateCount: 3, OnlineCount: 1, MaxOnline: 1}, nil
}
func (*fakeMaintenance) AimiliVPN(context.Context) (maintenance.AimiliSummary, error) {
	return maintenance.AimiliSummary{CandidateCount: 3}, nil
}
func (fake *fakeMaintenance) CandidateCountries(context.Context) ([]aimili.CandidateCountry, error) {
	return append([]aimili.CandidateCountry(nil), fake.countries...), nil
}
func (fake *fakeMaintenance) StartAimiliVPNRefresh(_ context.Context, country string) (aimili.CountryRefresh, error) {
	fake.startedCountries = append(fake.startedCountries, country)
	return fake.startResult, fake.startErr
}
func (fake *fakeMaintenance) AimiliVPNRefresh(context.Context) (aimili.CountryRefresh, error) {
	return fake.refresh, nil
}
func (*fakeMaintenance) CheckAimiliVPN(context.Context) (maintenance.AimiliSummary, error) {
	return maintenance.AimiliSummary{CandidateCount: 3}, nil
}
func (*fakeMaintenance) XUI(context.Context) (maintenance.XUISummary, error) {
	return maintenance.XUISummary{OwnershipMatches: true}, nil
}
func (*fakeMaintenance) CheckXUI(context.Context) (maintenance.XUISummary, error) {
	return maintenance.XUISummary{OwnershipMatches: true}, nil
}
func (*fakeMaintenance) RepairXUI(context.Context) (maintenance.XUISummary, error) {
	return maintenance.XUISummary{OwnershipMatches: true}, nil
}
