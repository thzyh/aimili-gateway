package xui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type adminXUIFixture struct {
	mu            sync.Mutex
	username      string
	password      string
	requireTOTP   bool
	cookieNames   []string
	updateCalls   int
	lastUpdate    map[string]string
	redirectLogin bool
}

func (fixture *adminXUIFixture) handler(response http.ResponseWriter, request *http.Request) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	response.Header().Set("Content-Type", "application/json")
	switch request.URL.Path {
	case "/panel/csrf-token":
		fmt.Fprint(response, `{"success":true,"obj":"csrf-admin"}`)
	case "/panel/login":
		if fixture.redirectLogin {
			response.Header().Set("Location", "https://unexpected.example.test/")
			response.WriteHeader(http.StatusFound)
			return
		}
		var input map[string]string
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(response, `{"success":false,"msg":"invalid request","obj":null}`)
			return
		}
		if fixture.requireTOTP {
			fmt.Fprint(response, `{"success":false,"msg":"two factor code required","obj":null}`)
			return
		}
		if input["username"] != fixture.username || input["password"] != fixture.password {
			fmt.Fprint(response, `{"success":false,"msg":"credentials rejected","obj":null}`)
			return
		}
		for _, name := range fixture.cookieNames {
			http.SetCookie(response, &http.Cookie{Name: name, Value: "opaque-browser-session", Path: "/panel/", HttpOnly: true})
		}
		fmt.Fprint(response, `{"success":true,"obj":null}`)
	case "/panel/panel/api/setting/updateUser":
		if request.Header.Get("X-CSRF-Token") != "csrf-admin" {
			response.WriteHeader(http.StatusForbidden)
			fmt.Fprint(response, `{"success":false,"msg":"csrf rejected","obj":null}`)
			return
		}
		var input map[string]string
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(response, `{"success":false,"msg":"invalid request","obj":null}`)
			return
		}
		fixture.updateCalls++
		fixture.lastUpdate = input
		if input["oldUsername"] != fixture.username || input["oldPassword"] != fixture.password {
			fmt.Fprint(response, `{"success":false,"msg":"credentials rejected","obj":null}`)
			return
		}
		fixture.username = input["newUsername"]
		fixture.password = input["newPassword"]
		fmt.Fprint(response, `{"success":true,"obj":{}}`)
	default:
		http.NotFound(response, request)
	}
}

func newAdminXUIClient(t *testing.T, fixture *adminXUIFixture) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/panel/", Credentials{Username: fixture.username, Password: fixture.password})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestVerifyAdminAuthenticatesWithExplicitCredentials(t *testing.T) {
	fixture := &adminXUIFixture{username: "owner", password: "old-password-marker", cookieNames: []string{"session"}}
	client := newAdminXUIClient(t, fixture)
	if err := client.VerifyAdmin(context.Background(), Credentials{Username: "owner", Password: "old-password-marker"}); err != nil {
		t.Fatal(err)
	}
	err := client.VerifyAdmin(context.Background(), Credentials{Username: "owner", Password: "wrong-password-marker"})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "credentials_rejected" || strings.Contains(err.Error(), "wrong-password-marker") {
		t.Fatalf("unexpected credential error: %v", err)
	}
}

func TestUpdateAdminUsesVersionedEndpointAndVerifiesNewCredentials(t *testing.T) {
	fixture := &adminXUIFixture{username: "owner", password: "old-password-marker", cookieNames: []string{"session"}}
	client := newAdminXUIClient(t, fixture)
	current := Credentials{Username: "owner", Password: "old-password-marker"}
	next := Credentials{Username: "renamed", Password: "new-password-marker"}
	if err := client.UpdateAdmin(context.Background(), current, next); err != nil {
		t.Fatal(err)
	}
	if fixture.updateCalls != 1 || len(fixture.lastUpdate) != 4 {
		t.Fatalf("update calls=%d body=%#v", fixture.updateCalls, fixture.lastUpdate)
	}
	for key, want := range map[string]string{
		"oldUsername": "owner",
		"oldPassword": "old-password-marker",
		"newUsername": "renamed",
		"newPassword": "new-password-marker",
	} {
		if fixture.lastUpdate[key] != want {
			t.Fatalf("%s = %q", key, fixture.lastUpdate[key])
		}
	}
	if err := client.VerifyAdmin(context.Background(), next); err != nil {
		t.Fatalf("new credentials were not verified: %v", err)
	}
}

func TestIssueAdminSessionReturnsOnlyWhitelistedCookie(t *testing.T) {
	fixture := &adminXUIFixture{username: "owner", password: "old-password-marker", cookieNames: []string{"session"}}
	client := newAdminXUIClient(t, fixture)
	session, err := client.IssueAdminSession(context.Background(), Credentials{Username: "owner", Password: "old-password-marker"})
	if err != nil {
		t.Fatal(err)
	}
	if session.CookieName != "session" || string(session.Token) != "opaque-browser-session" {
		t.Fatalf("browser session = %#v", session)
	}
}

func TestIssueAdminSessionFailsClosedForUnknownCookieTOTPAndRedirect(t *testing.T) {
	for name, test := range map[string]struct {
		fixture *adminXUIFixture
		code    string
	}{
		"unknown cookie": {&adminXUIFixture{username: "owner", password: "old-password-marker", cookieNames: []string{"session", "tracking"}}, "unexpected_cookie"},
		"TOTP":           {&adminXUIFixture{username: "owner", password: "old-password-marker", requireTOTP: true}, "totp_incompatible"},
		"redirect":       {&adminXUIFixture{username: "owner", password: "old-password-marker", redirectLogin: true}, "unsafe_redirect"},
	} {
		t.Run(name, func(t *testing.T) {
			client := newAdminXUIClient(t, test.fixture)
			_, err := client.IssueAdminSession(context.Background(), Credentials{Username: "owner", Password: "old-password-marker"})
			var adapterError *AdapterError
			if !errors.As(err, &adapterError) || adapterError.Code != test.code {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestProbeAdminCapabilitiesRejectsConfiguredTOTP(t *testing.T) {
	fixture := &adminXUIFixture{username: "owner", password: "old-password-marker", cookieNames: []string{"session"}}
	client := newAdminXUIClient(t, fixture)
	client.credentials.TwoFactorCode = "123456"
	capabilities, err := client.ProbeAdminCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.TOTPCompatible || capabilities.CanUpdate || capabilities.CanBridge {
		t.Fatalf("TOTP capabilities = %#v", capabilities)
	}
}
