package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/auth"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestAuthRejectsWrongCredentials(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	for _, testCase := range []struct {
		name     string
		password string
		totp     string
	}{
		{name: "wrong password", password: "different-local-test-password", totp: "287082"},
		{name: "wrong TOTP", password: "local-only-test-password", totp: "000000"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := environment.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
				"username": "owner",
				"password": testCase.password,
				"totp":     testCase.totp,
			}, environment.origin, "")
			assertResponseStatus(t, response, http.StatusUnauthorized)
		})
	}
}

func TestLoginBeforeInitializationIsUnauthorized(t *testing.T) {
	environment := newAuthTestEnvironmentWithAdmin(t, false)
	response := environment.request(t, http.MethodPost, "/api/v1/auth/login", validLoginPayload(), environment.origin, "")
	assertResponseStatus(t, response, http.StatusUnauthorized)
}

func TestAuthOptionsExposeOnlyWhetherTOTPIsRequired(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	response := environment.request(t, http.MethodGet, "/api/v1/auth/options", nil, "", "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("auth options response is cacheable")
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload["totpRequired"] != true {
		t.Fatalf("auth options payload = %#v", payload)
	}
	if _, exposed := payload["username"]; exposed {
		t.Fatal("auth options exposed username")
	}
}

func TestPasswordOnlyAccountCanLoginAndReauthenticateWithoutTOTP(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	environment.disableTOTP(t)
	login := environment.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "owner",
		"password": "local-only-test-password",
	}, environment.origin, "")
	assertResponseStatus(t, login, http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	response := environment.request(t, http.MethodPost, "/api/v1/auth/reauth", map[string]string{
		"password": "local-only-test-password",
	}, environment.origin, csrf)
	assertResponseStatus(t, response, http.StatusNoContent)
}

func TestTOTPAccountRejectsMissingTOTP(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	response := environment.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "owner",
		"password": "local-only-test-password",
	}, environment.origin, "")
	assertResponseStatus(t, response, http.StatusBadRequest)
}

func TestSuccessfulLoginSetsSecureCookieAndReturnsSession(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	response := environment.login(t)
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == "aimili_gateway_session" {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie missing")
	}
	if !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Path != "/" {
		t.Fatal("session cookie security attributes are incomplete")
	}
	assertResponseStatus(t, response, http.StatusNoContent)

	session := environment.session(t)
	if !session.Authenticated || session.CSRFToken == "" {
		t.Fatal("authenticated session response is incomplete")
	}
	if _, err := base64.RawURLEncoding.DecodeString(session.CSRFToken); err != nil {
		t.Fatal("CSRF token is not base64url")
	}
}

func TestMutationRequiresCSRFAndSameOrigin(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken

	withoutCSRF := environment.request(t, http.MethodPost, "/api/v1/auth/logout", nil, environment.origin, "")
	assertResponseStatus(t, withoutCSRF, http.StatusForbidden)

	wrongOrigin := environment.request(t, http.MethodPost, "/api/v1/auth/logout", nil, "https://different.example.test", csrf)
	assertResponseStatus(t, wrongOrigin, http.StatusForbidden)

	valid := environment.request(t, http.MethodPost, "/api/v1/auth/logout", nil, environment.origin, csrf)
	assertResponseStatus(t, valid, http.StatusNoContent)
	assertResponseStatus(t, environment.request(t, http.MethodGet, "/api/v1/auth/session", nil, "", ""), http.StatusUnauthorized)
}

func TestLoginRejectsWrongOrigin(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	response := environment.request(t, http.MethodPost, "/api/v1/auth/login", validLoginPayload(), "https://different.example.test", "")
	assertResponseStatus(t, response, http.StatusForbidden)
	if len(environment.client.Jar.Cookies(environment.baseURL)) != 0 {
		t.Fatal("origin rejection created a cookie")
	}
}

func TestSessionExpiresAfterIdleTimeout(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	environment.clock.Advance(30 * time.Minute)
	response := environment.request(t, http.MethodGet, "/api/v1/auth/session", nil, "", "")
	assertResponseStatus(t, response, http.StatusUnauthorized)
}

func TestSessionExpiresAtAbsoluteLifetime(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	tokenHash := environment.sessionTokenHash(t)
	session, err := environment.database.GetSession(context.Background(), tokenHash, environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	environment.clock.Advance(12*time.Hour - time.Minute)
	if err := environment.database.TouchSession(context.Background(), session.ID, environment.clock.Now()); err != nil {
		t.Fatal(err)
	}
	environment.clock.Advance(time.Minute)
	response := environment.request(t, http.MethodGet, "/api/v1/auth/session", nil, "", "")
	assertResponseStatus(t, response, http.StatusUnauthorized)
}

func TestReauthenticationRecordsFreshTimestamp(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	environment.clock.Advance(time.Second)
	response := environment.request(t, http.MethodPost, "/api/v1/auth/reauth", map[string]string{
		"password": "local-only-test-password",
		"totp":     "287082",
	}, environment.origin, csrf)
	assertResponseStatus(t, response, http.StatusNoContent)

	session, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if session.ReauthenticatedAt == nil || !session.ReauthenticatedAt.Equal(environment.clock.Now()) {
		t.Fatal("reauthentication timestamp was not stored")
	}
}

func TestLoginThrottlesAfterFiveFailures(t *testing.T) {
	environment := newAuthTestEnvironment(t)
	for attempt := 0; attempt < 5; attempt++ {
		response := environment.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"username": "owner",
			"password": "different-local-test-password",
			"totp":     "287082",
		}, environment.origin, "")
		assertResponseStatus(t, response, http.StatusUnauthorized)
	}
	response := environment.request(t, http.MethodPost, "/api/v1/auth/login", validLoginPayload(), environment.origin, "")
	assertResponseStatus(t, response, http.StatusTooManyRequests)
}

type authTestEnvironment struct {
	database *store.Store
	server   *httptest.Server
	client   *http.Client
	baseURL  *url.URL
	origin   string
	clock    *authTestClock
}

func newAuthTestEnvironment(t *testing.T) *authTestEnvironment {
	return newAuthTestEnvironmentConfigured(t, true, nil)
}

func newAuthTestEnvironmentWithAdmin(t *testing.T, initializeAdmin bool) *authTestEnvironment {
	return newAuthTestEnvironmentConfigured(t, initializeAdmin, nil)
}

func newAuthTestEnvironmentConfigured(t *testing.T, initializeAdmin bool, configure func(*Dependencies)) *authTestEnvironment {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{1}, 32)
	passwordHash, err := auth.HashPassword([]byte("local-only-test-password"))
	if err != nil {
		t.Fatal(err)
	}
	encryptedSecret, err := auth.Seal(masterKey, []byte("12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &authTestClock{current: time.Unix(59, 0).UTC()}
	if initializeAdmin {
		if err := database.CreateAdmin(ctx, store.Admin{
			Username:             "owner",
			PasswordHash:         []byte(passwordHash),
			TOTPEnabled:          true,
			TOTPSecretCiphertext: encryptedSecret,
			CreatedAt:            clock.Now(),
			SecurityUpdatedAt:    clock.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	dependencies := Dependencies{
		Store:      database,
		MasterKey:  masterKey,
		TestOrigin: origin,
		Now:        clock.Now,
	}
	if configure != nil {
		configure(&dependencies)
	}
	server.Config.Handler = NewServer(dependencies)
	server.StartTLS()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	environment := &authTestEnvironment{
		database: database,
		server:   server,
		client:   client,
		baseURL:  baseURL,
		origin:   origin,
		clock:    clock,
	}
	t.Cleanup(func() {
		server.Close()
		_ = database.Close()
	})
	return environment
}

func (e *authTestEnvironment) disableTOTP(t *testing.T) {
	t.Helper()
	admin, err := e.database.GetAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.database.UpdateAdminSecurityAndRevokeSessions(context.Background(), store.AdminSecurityUpdate{
		ExpectedSecurityUpdatedAt: admin.SecurityUpdatedAt,
		Username:                  admin.Username,
		PasswordHash:              admin.PasswordHash,
		TOTPEnabled:               false,
		Action:                    "test.totp_disabled",
		UpdatedAt:                 admin.SecurityUpdatedAt.Add(time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *authTestEnvironment) login(t *testing.T) *http.Response {
	t.Helper()
	return e.request(t, http.MethodPost, "/api/v1/auth/login", validLoginPayload(), e.origin, "")
}

func validLoginPayload() map[string]string {
	return map[string]string{
		"username": "owner",
		"password": "local-only-test-password",
		"totp":     "287082",
	}
}

func (e *authTestEnvironment) request(t *testing.T, method, path string, payload any, origin, csrf string) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, e.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := e.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (e *authTestEnvironment) session(t *testing.T) sessionPayload {
	t.Helper()
	response := e.request(t, http.MethodGet, "/api/v1/auth/session", nil, "", "")
	if response.StatusCode != http.StatusOK {
		assertResponseStatus(t, response, http.StatusOK)
	}
	defer response.Body.Close()
	var payload sessionPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func (e *authTestEnvironment) sessionTokenHash(t *testing.T) [32]byte {
	t.Helper()
	for _, cookie := range e.client.Jar.Cookies(e.baseURL) {
		if cookie.Name != "aimili_gateway_session" {
			continue
		}
		parts := strings.Split(cookie.Value, ".")
		if len(parts) != 2 {
			t.Fatal("invalid session cookie format")
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil || len(raw) != 32 {
			t.Fatal("invalid session token encoding")
		}
		return sha256.Sum256(raw)
	}
	t.Fatal("session cookie missing")
	return [32]byte{}
}

type sessionPayload struct {
	Authenticated bool      `json:"authenticated"`
	CSRFToken     string    `json:"csrfToken"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

func assertResponseStatus(t *testing.T, response *http.Response, expected int) {
	t.Helper()
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != expected {
		t.Fatalf("status = %d, expected %d", response.StatusCode, expected)
	}
}

type authTestClock struct {
	mu      sync.Mutex
	current time.Time
}

func (c *authTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *authTestClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.current = c.current.Add(duration)
	c.mu.Unlock()
}
