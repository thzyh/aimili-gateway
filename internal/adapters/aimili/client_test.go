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

func TestClientMainStatusReadsSafeMainEgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/control/v1/main" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		fmt.Fprint(response, `{"data":{"country":"JP","country_name":"Japan","proxy_type":"datacenter","exit_ip":"203.0.113.20","port":7928,"egress_ok":true,"active":true}}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.MainStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Country != "JP" || status.Port != 7928 || status.ExitIP != "203.0.113.20" || !status.EgressOK {
		t.Fatalf("main status = %#v", status)
	}
}

func TestClientMutationLeaseUsesClosedAcquireRenewReleaseContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/mutation-leases" {
				t.Fatalf("acquire request = %s %s", request.Method, request.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body) != 1 || body["idempotencyKey"] != "protocol-operation-safe-1" {
				t.Fatalf("acquire body = %#v, err = %v", body, err)
			}
			response.WriteHeader(http.StatusCreated)
			fmt.Fprint(response, `{"data":{"state":"active","lease_id":"opaque+lease=safe","expires_at":1700000060}}`)
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/mutation-leases/opaque+lease=safe/renew" {
				t.Fatalf("renew request = %s %s", request.Method, request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body) != 0 {
				t.Fatalf("renew body = %#v, err = %v", body, err)
			}
			fmt.Fprint(response, `{"data":{"state":"active","lease_id":"opaque+lease=safe","expires_at":1700000120}}`)
		case 3:
			if request.Method != http.MethodDelete || request.URL.Path != "/control/v1/mutation-leases/opaque+lease=safe" || request.Body != nil && request.ContentLength != 0 {
				t.Fatalf("release request = %s %s length=%d", request.Method, request.URL.Path, request.ContentLength)
			}
			fmt.Fprint(response, `{"data":{"state":"released"}}`)
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}

	lease, err := client.AcquireMutationLease(context.Background(), "protocol-operation-safe-1")
	if err != nil || lease.LeaseID != "opaque+lease=safe" || lease.ExpiresAt != 1700000060 {
		t.Fatalf("acquire = %#v, err = %v", lease, err)
	}
	lease, err = client.RenewMutationLease(context.Background(), lease.LeaseID)
	if err != nil || lease.ExpiresAt != 1700000120 {
		t.Fatalf("renew = %#v, err = %v", lease, err)
	}
	if err := client.ReleaseMutationLease(context.Background(), lease.LeaseID); err != nil {
		t.Fatal(err)
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

func TestClientAssignSlotNodeUsesVersionedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/control/v1/slots/2/assign" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["candidateId"] != "node-safe" || body["country"] != "JP" || body["proxyType"] != "datacenter" {
			t.Fatalf("assign body = %#v", body)
		}
		fmt.Fprint(response, `{"data":{"slot":2,"country":"JP","proxy_type":"datacenter","port":17930,"status":"up","node_id":"node-safe","egress_ok":true}}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	slot, err := client.AssignSlotNode(context.Background(), 2, AssignSlotRequest{CandidateID: "node-safe", Country: "jp", ProxyType: "datacenter"})
	if err != nil {
		t.Fatal(err)
	}
	if slot.Number != 2 || slot.NodeID != "node-safe" {
		t.Fatalf("slot = %#v", slot)
	}
}

func TestClientListsSafeManagedSlots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/control/v1/slots" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"data":[{"slot":2,"country":"JP","country_name":"Japan","proxy_type":"residential","port":17930,"status":"up","node_id":"node-safe","candidate_ip":"198.51.100.10","exit_ip":"203.0.113.5","egress_ok":true,"ok":true,"latency_ms":42,"checked_at":1700000000}]}`)
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
	if len(slots) != 1 || slots[0].NodeID != "node-safe" || !slots[0].OK {
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

func TestClientVerifyAdminUsesClosedPOSTAndNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/control/v1/admin/verify" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 2 || body["username"] != "owner" || body["password"] != "old-password-marker" {
			t.Fatalf("verify body = %#v", body)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.VerifyAdmin(context.Background(), AdminUpdate{Username: "owner", Password: []byte("old-password-marker")}); err != nil {
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

func TestClientCountryRefreshUsesVersionedClosedRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing control bearer token")
		}
		response.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if request.Method != http.MethodGet || request.URL.Path != "/control/v1/candidates/countries" {
				t.Fatalf("unexpected countries request %s %s", request.Method, request.URL.Path)
			}
			fmt.Fprint(response, `{"data":[{"code":"JP","name":"日本","candidateCount":8,"observedAt":1700000000,"futureField":"ignored"}]}`)
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/candidates/refresh" {
				t.Fatalf("unexpected refresh request %s %s", request.Method, request.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || body["country"] != "JP" {
				t.Fatalf("refresh body = %#v", body)
			}
			response.WriteHeader(http.StatusAccepted)
			fmt.Fprint(response, `{"data":{"state":"running","country":"JP","phase":"fetching","catalogCount":0,"countryCandidateCount":0,"testedCount":0,"validCount":0,"preservedCount":0,"startedAt":1700000000,"finishedAt":0,"errorCode":"","futureField":"ignored"}}`)
		case 3:
			if request.Method != http.MethodGet || request.URL.Path != "/control/v1/candidates/refresh" {
				t.Fatalf("unexpected status request %s %s", request.Method, request.URL.Path)
			}
			fmt.Fprint(response, `{"data":{"state":"completed","country":"JP","phase":"","catalogCount":20,"countryCandidateCount":8,"testedCount":5,"validCount":4,"preservedCount":1,"startedAt":1700000000,"finishedAt":1700000010,"errorCode":""}}`)
		default:
			t.Fatalf("unexpected extra request %d", requests)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	countries, err := client.CandidateCountries(context.Background())
	if err != nil || len(countries) != 1 || countries[0].Code != "JP" || countries[0].CandidateCount != 8 {
		t.Fatalf("countries = %#v, err = %v", countries, err)
	}
	started, err := client.StartCountryRefresh(context.Background(), "jp")
	if err != nil || started.State != "running" || started.Country != "JP" {
		t.Fatalf("started = %#v, err = %v", started, err)
	}
	status, err := client.CountryRefresh(context.Background())
	if err != nil || status.State != "completed" || status.TestedCount != 5 || status.ValidCount != 4 {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestClientCountryRefreshMapsSafeErrorsAndRejectsMissingData(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{name: "busy", status: http.StatusConflict, body: `{"error":{"code":"maintenance_busy"},"detail":"secret-upstream-text"}`, wantCode: "maintenance_busy"},
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":{"code":"unauthorized"}}`, wantCode: "unauthorized"},
		{name: "missing data", status: http.StatusAccepted, body: `{}`, wantCode: "invalid_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.status)
				fmt.Fprint(response, test.body)
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL+"/", []byte("test-token"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.StartCountryRefresh(context.Background(), "JP")
			var adapterError *AdapterError
			if !errors.As(err, &adapterError) || adapterError.Code != test.wantCode {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "secret-upstream-text") {
				t.Fatal("adapter error exposed upstream response")
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
