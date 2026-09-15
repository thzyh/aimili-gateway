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
		fmt.Fprint(response, `{"data":[{"id":"node-safe","country_short":"JP","country":"Japan","ip":"198.51.100.10","exit_ip":"203.0.113.10","exit_ip_checked_at":1700000005,"proxy_type":"datacenter","owner":"Example","asn":"AS64500","as_name":"Example","latency_ms":42,"score":9,"probe_status":"available","last_probe_at":1700000000},{"id":"node-no-exit","country_short":"US","country":"United States","ip":"198.51.100.11","proxy_type":"residential","probe_status":"available"}]}`)
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
	if len(candidates) != 2 || candidates[0].ProxyType != "datacenter" || candidates[0].CountryCode != "JP" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
	if candidates[0].IP != "198.51.100.10" || candidates[0].ExitIP != "203.0.113.10" || candidates[0].ExitIPCheckedAt != 1700000005 {
		t.Fatalf("candidate entry and exit addresses were not kept separate: %#v", candidates[0])
	}
	if candidates[1].ExitIP != "" || candidates[1].ExitIPCheckedAt != 0 {
		t.Fatalf("missing verified exit was fabricated: %#v", candidates[1])
	}
}

func TestClientCandidatesAcceptsFiftyNodePoolResponse(t *testing.T) {
	candidates := make([]Candidate, 50)
	for index := range candidates {
		candidates[index] = Candidate{
			ID:              fmt.Sprintf("US_198.51.100.%d_443_tcp", index+1),
			CountryCode:     "US",
			CountryName:     "United States",
			IP:              fmt.Sprintf("198.51.100.%d", index+1),
			ExitIP:          fmt.Sprintf("203.0.113.%d", index+1),
			ExitIPCheckedAt: 1_700_000_005,
			ProxyType:       "residential",
			Owner:           strings.Repeat("Example owner ", 5),
			ASN:             "AS64500",
			ASName:          strings.Repeat("Example network ", 5),
			LatencyMS:       42,
			Score:           9,
			ProbeStatus:     "available",
			LastProbeAt:     1_700_000_000,
		}
	}
	payload, err := json.Marshal(struct {
		Data []Candidate `json:"data"`
	}{Data: candidates})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 16<<10 || len(payload) > 64<<10 {
		t.Fatalf("fixture size = %d, want a realistic expanded-pool response", len(payload))
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(payload)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Candidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 50 {
		t.Fatalf("candidate count = %d, want 50", len(result))
	}
}

func TestClientCandidatesRejectsInvalidExitMetadata(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing candidate id", body: `{"data":[{"country_short":"JP","ip":"198.51.100.10","proxy_type":"datacenter","probe_status":"available"}]}`},
		{name: "missing candidate ip", body: `{"data":[{"id":"node","country_short":"JP","proxy_type":"datacenter","probe_status":"available"}]}`},
		{name: "invalid candidate ip", body: `{"data":[{"id":"node","country_short":"JP","ip":"not-an-ip","proxy_type":"datacenter","probe_status":"available"}]}`},
		{name: "invalid exit ip", body: `{"data":[{"id":"node","country_short":"JP","ip":"198.51.100.10","exit_ip":"not-an-ip","proxy_type":"datacenter","probe_status":"available"}]}`},
		{name: "oversized exit ip", body: `{"data":[{"id":"node","country_short":"JP","ip":"198.51.100.10","exit_ip":"` + strings.Repeat("1", 65) + `","proxy_type":"datacenter","probe_status":"available"}]}`},
		{name: "negative exit check", body: `{"data":[{"id":"node","country_short":"JP","ip":"198.51.100.10","exit_ip":"203.0.113.10","exit_ip_checked_at":-1,"proxy_type":"datacenter","probe_status":"available"}]}`},
		{name: "wrong exit check type", body: `{"data":[{"id":"node","country_short":"JP","ip":"198.51.100.10","exit_ip_checked_at":"now","proxy_type":"datacenter","probe_status":"available"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(response, test.body)
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL+"/", []byte("test-token"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Candidates(context.Background()); err == nil {
				t.Fatal("invalid candidate response was accepted")
			}
		})
	}
}

func TestClientPreservesSafeCandidateRejectedErrorField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusConflict)
		fmt.Fprint(response, `{"error":{"code":"candidate_dial_failed","candidateRejected":true}}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AssignSlotNode(context.Background(), 2, AssignSlotRequest{CandidateID: "node", Country: "JP", ProxyType: "datacenter"})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "candidate_dial_failed" || !adapterError.CandidateRejected {
		t.Fatalf("candidate rejection metadata was lost: %#v", err)
	}
}

func TestClientMainStatusReadsSafeMainEgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/control/v1/main" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		fmt.Fprint(response, `{"data":{"country":"JP","country_name":"Japan","proxy_type":"datacenter","exit_ip":"203.0.113.20","port":7928,"egress_ok":true,"active":true,"repair_status":"healthy","auto_repair_attempted":false,"last_error_code":""}}`)
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
	if status.Country != "JP" || status.Port != 7928 || status.ExitIP != "203.0.113.20" || !status.EgressOK || status.RepairStatus != "healthy" || status.AutoRepairAttempted {
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
		fmt.Fprint(response, `{"data":[{"slot":2,"country":"JP","country_name":"Japan","proxy_type":"residential","port":17930,"status":"disconnected","node_id":"node-safe","candidate_ip":"198.51.100.10","exit_ip":"203.0.113.5","egress_ok":false,"ok":false,"latency_ms":42,"checked_at":1700000000,"repair_status":"manual_required","auto_repair_attempted":true,"auto_repair_performed":true,"last_error_code":"replacement_failed"}]}`)
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
	if len(slots) != 1 || slots[0].NodeID != "node-safe" || slots[0].OK || slots[0].RepairStatus != "manual_required" || !slots[0].AutoRepairAttempted || !slots[0].AutoRepairPerformed || slots[0].LastErrorCode != "replacement_failed" {
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
		case "/control/v1/slots/0/check":
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
	if _, err := client.CheckSlot(context.Background(), 0); err != nil {
		t.Fatalf("slot check did not use the long operation timeout: %v", err)
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
		{name: "oversized", body: `{"data":[]}` + strings.Repeat(" ", 65<<10)},
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
			fmt.Fprint(response, `{"data":[{"code":"JP","name":"日本","candidateCount":8,"observedAt":1700000000,"officialCandidateTotal":100,"validNodeCount":25,"validCountryCount":5,"targetValidNodeCount":64,"maxValidNodeCount":80,"futureField":"ignored"}]}`)
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
			fmt.Fprint(response, `{"data":{"state":"completed","country":"JP","phase":"","resultCode":"success","catalogCount":20,"officialCount":8,"countryCandidateCount":8,"testedCount":5,"usableCount":4,"newUsableCount":3,"retainedCount":1,"validCount":4,"preservedCount":1,"cacheTotal":66,"countryValidCount":4,"targetValidNodeCount":64,"maxValidNodeCount":80,"startedAt":1700000000,"finishedAt":1700000010,"errorCode":""}}`)
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
	if err != nil || len(countries) != 1 || countries[0].Code != "JP" || countries[0].CandidateCount != 8 || countries[0].OfficialCandidateTotal != 100 || countries[0].ValidNodeCount != 25 || countries[0].ValidCountryCount != 5 || countries[0].TargetValidNodeCount != 64 || countries[0].MaxValidNodeCount != 80 {
		t.Fatalf("countries = %#v, err = %v", countries, err)
	}
	started, err := client.StartCountryRefresh(context.Background(), "jp")
	if err != nil || started.State != "running" || started.Country != "JP" {
		t.Fatalf("started = %#v, err = %v", started, err)
	}
	status, err := client.CountryRefresh(context.Background())
	if err != nil || status.State != "completed" || status.ResultCode != "success" || status.OfficialCount != 8 || status.TestedCount != 5 || status.UsableCount != 4 || status.NewUsableCount != 3 || status.RetainedCount != 1 || status.ValidCount != 4 || status.TargetValidNodeCount != 64 || status.MaxValidNodeCount != 80 {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestCountryRefreshAcceptsOnlyClosedResultCodesAndNonnegativeCounts(t *testing.T) {
	validCodes := []string{"", "success", "no_official_candidates", "no_usable_nodes", "operation_busy", "maintenance_busy", "upstream_unavailable"}
	for _, code := range validCodes {
		refresh := CountryRefresh{State: "completed", Country: "JP", ResultCode: code}
		if !validCountryRefresh(refresh) {
			t.Fatalf("valid result code %q was rejected", code)
		}
	}
	invalid := []CountryRefresh{
		{State: "completed", Country: "JP", ResultCode: "refresh_failed"},
		{State: "completed", Country: "JP", ResultCode: strings.Repeat("x", 65)},
		{State: "completed", Country: "JP", OfficialCount: -1},
		{State: "completed", Country: "JP", UsableCount: -1},
		{State: "completed", Country: "JP", RetainedCount: -1},
		{State: "completed", Country: "JP", NewUsableCount: -1},
		{State: "completed", Country: "JP", TargetValidNodeCount: -1},
		{State: "completed", Country: "JP", MaxValidNodeCount: -1},
		{State: "completed", Country: "JP", StartedAt: -1},
	}
	for _, refresh := range invalid {
		if validCountryRefresh(refresh) {
			t.Fatalf("invalid refresh was accepted: %#v", refresh)
		}
	}
}

func TestClientCountryRefreshAcceptsAllCountryScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["country"] != "ALL" {
			t.Fatalf("refresh body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusAccepted)
		fmt.Fprint(response, `{"data":{"state":"running","country":"ALL","phase":"fetching","testedCount":0,"validCount":0}}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	started, err := client.StartCountryRefresh(context.Background(), "all")
	if err != nil || started.Country != "ALL" || started.State != "running" {
		t.Fatalf("started = %#v, err = %v", started, err)
	}
}

func TestClientCountryRefreshRejectsWrongStructuredFieldTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{"data":{"state":"completed","country":"JP","resultCode":"success","officialCount":"eight"}}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CountryRefresh(context.Background()); err == nil {
		t.Fatal("wrong structured refresh field type was accepted")
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
