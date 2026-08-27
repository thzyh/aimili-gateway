package aimili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClientCandidatesSendsBearerTokenAndDecodesSafeFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/control/v1/candidates" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing control bearer token")
		}
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"data":[{"id":"node-safe","country_short":"JP","country":"Japan","ip":"198.51.100.10","proxy_type":"datacenter","owner":"Example","asn":"AS64500","as_name":"Example","latency_ms":42,"score":9,"probe_status":"available","last_probe_at":1700000000}]}`)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := client.Candidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ProxyType != "datacenter" || candidates[0].CountryCode != "JP" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
}

func TestClientCreateSlotUsesClosedRequestAndResponseTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/control/v1/slots" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["candidateId"] != "node-safe" {
			t.Fatalf("candidateId = %#v", body["candidateId"])
		}
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"data":{"slot":2,"country":"JP","country_name":"Japan","proxy_type":"residential","port":17930,"status":"up","node_id":"node-safe","candidate_ip":"198.51.100.10","exit_ip":"203.0.113.5","egress_ok":true,"latency_ms":42,"checked_at":1700000000}}`)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	slot, err := client.CreateSlot(context.Background(), CreateSlotRequest{Country: "JP", ProxyType: "residential", CandidateID: "node-safe"})
	if err != nil {
		t.Fatal(err)
	}
	if slot.Number != 2 || slot.Port != 17930 || slot.ExitIP != "203.0.113.5" {
		t.Fatalf("unexpected slot: %#v", slot)
	}
}

func TestClientListsSafeManagedSlots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/control/v1/slots" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"data":[{"slot":2,"country":"JP","country_name":"Japan","proxy_type":"residential","port":17930,"status":"up","node_id":"node-safe","candidate_ip":"198.51.100.10","exit_ip":"203.0.113.5","egress_ok":true,"latency_ms":42,"checked_at":1700000000}]}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	slots, err := client.ListSlots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0].NodeID != "node-safe" {
		t.Fatalf("unexpected slots: %#v", slots)
	}
}

func TestClientLongOperationsOutliveReadTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		time.Sleep(60 * time.Millisecond)
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/control/v1/candidates":
			fmt.Fprint(response, `{"data":[]}`)
		case "/control/v1/slots":
			fmt.Fprint(response, `{"data":{"slot":0,"country":"JP","country_name":"Japan","proxy_type":"datacenter","port":17928,"status":"up","node_id":"node-a","candidate_ip":"198.51.100.10","exit_ip":"203.0.113.10","egress_ok":true,"latency_ms":42,"checked_at":1700000000}}`)
		case "/control/v1/slots/0/rotate":
			fmt.Fprint(response, `{"data":{"slot":0,"country":"JP","country_name":"Japan","proxy_type":"datacenter","port":17928,"status":"up","node_id":"node-b","candidate_ip":"198.51.100.11","exit_ip":"203.0.113.11","egress_ok":true,"latency_ms":44,"checked_at":1700000001}}`)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	client.readTimeout = 20 * time.Millisecond
	client.operationTimeout = 200 * time.Millisecond

	if _, err := client.Candidates(context.Background()); err == nil {
		t.Fatal("ordinary read outlived its short timeout")
	}
	if _, err := client.CreateSlot(context.Background(), CreateSlotRequest{Country: "JP", ProxyType: "datacenter"}); err != nil {
		t.Fatalf("create slot did not use the long operation timeout: %v", err)
	}
	if _, err := client.RotateSlot(context.Background(), 0); err != nil {
		t.Fatalf("rotate slot did not use the long operation timeout: %v", err)
	}
}

func TestClientMapsUpstreamErrorWithoutExposingResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusConflict)
		fmt.Fprint(response, `{"error":{"code":"no_matching_candidate"},"secret":"must-not-leak"}`)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateSlot(context.Background(), CreateSlotRequest{Country: "JP", ProxyType: "datacenter"})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "no_matching_candidate" {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Fatal("adapter error exposed upstream response")
	}
}

func TestClientRejectsOversizedAndUnknownResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "oversized", body: `{"data":[]}` + strings.Repeat(" ", 17<<10)},
		{name: "unknown field", body: `{"data":[],"unexpected":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				fmt.Fprint(response, test.body)
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL+"/", []byte("test-token"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Candidates(context.Background()); err == nil {
				t.Fatal("invalid response was accepted")
			}
		})
	}
}

func TestClientRejectsNonLoopbackURLAndEmptyToken(t *testing.T) {
	if _, err := NewClient("http://192.0.2.10:8790/", []byte("token")); err == nil {
		t.Fatal("non-loopback control URL was accepted")
	}
	if _, err := NewClient("http://127.0.0.1:8790/", nil); err == nil {
		t.Fatal("empty token was accepted")
	}
}

func TestClientAdminStatusUsesBearerAndClosedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/control/v1/admin" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing control bearer token")
		}
		fmt.Fprint(response, `{"data":{"username":"owner","totpSupported":false}}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.AdminStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Username != "owner" || status.TOTPSupported {
		t.Fatalf("admin status = %#v", status)
	}
}

func TestClientUpdateAdminUsesClosedPUTAndNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/control/v1/admin" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 2 || body["username"] != "renamed" || body["password"] != "new-password-marker" {
			t.Fatalf("admin update body = %#v", body)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	password := []byte("new-password-marker")
	if err := client.UpdateAdmin(context.Background(), AdminUpdate{Username: "renamed", Password: password}); err != nil {
		t.Fatal(err)
	}
}

func TestClientIssueAdminSessionAcceptsOnlyExpectedOpaqueCookie(t *testing.T) {
	expiresAt := time.Now().UTC().Add(5 * time.Minute).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/control/v1/admin/sessions" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		fmt.Fprintf(response, `{"data":{"cookieName":"session","sessionToken":"opaque-session-token-with-enough-entropy","expiresAt":%d}}`, expiresAt)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.IssueAdminSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if session.CookieName != "session" || string(session.Token) != "opaque-session-token-with-enough-entropy" || session.ExpiresAt.Unix() != expiresAt {
		t.Fatalf("admin session = %#v", session)
	}
}

func TestClientIssueAdminSessionRejectsUnknownCookieAndFields(t *testing.T) {
	for name, body := range map[string]string{
		"unknown cookie": `{"data":{"cookieName":"other","sessionToken":"opaque-session-token-with-enough-entropy","expiresAt":1700000300}}`,
		"unknown field":  `{"data":{"cookieName":"session","sessionToken":"opaque-session-token-with-enough-entropy","expiresAt":1700000300,"secret_path":"forbidden"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				fmt.Fprint(response, body)
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL+"/", []byte("test-token"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.IssueAdminSession(context.Background()); err == nil {
				t.Fatal("invalid admin session was accepted")
			}
		})
	}
}

func TestReadTokenFileReturnsTrimmedSecretAndRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(token) != "file-token" {
		t.Fatalf("unexpected token length %d", len(token))
	}
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(path); err == nil {
		t.Fatal("empty control token was accepted")
	}
}

func TestReadTokenFileRejectsBroadUnixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	path := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(path); err == nil {
		t.Fatal("broad token file permissions were accepted")
	}
}
