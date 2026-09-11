package aimili

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientMainAssignmentTransactionUsesClosedContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing bearer token")
		}
		response.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if request.Method != http.MethodGet || request.URL.Path != "/control/v1/main/assignment" {
				t.Fatalf("assignment request = %s %s", request.Method, request.URL.Path)
			}
			fmt.Fprint(response, `{"data":{"state":"idle"}}`)
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/main/assign" {
				t.Fatalf("stage request = %s %s", request.Method, request.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 5 || body["candidateId"] != "candidate-new" || body["expectedCurrentCandidateId"] != "candidate-old" || body["idempotencyKey"] != "gateway-operation-1" {
				t.Fatalf("stage body = %#v", body)
			}
			response.WriteHeader(http.StatusAccepted)
			fmt.Fprint(response, `{"data":{"operation_id":"operation-safe-1","state":"pending_commit","old_candidate_id":"candidate-old","new_candidate_id":"candidate-new","country":"JP","proxy_type":"datacenter","port":7928,"dns_verified":true,"exit_verified":true,"available":true,"error_code":"","expires_at":1700000180}}`)
		case 3:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/main/assign/operation-safe-1/commit" {
				t.Fatalf("commit request = %s %s", request.Method, request.URL.Path)
			}
			fmt.Fprint(response, `{"data":{"operation_id":"operation-safe-1","state":"committed","old_candidate_id":"candidate-old","new_candidate_id":"candidate-new","country":"JP","proxy_type":"datacenter","port":7928,"dns_verified":true,"exit_verified":true,"available":true,"error_code":"","expires_at":1700000180}}`)
		case 4:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/main/assign/operation-safe-1/rollback" {
				t.Fatalf("rollback request = %s %s", request.Method, request.URL.Path)
			}
			fmt.Fprint(response, `{"data":{"operation_id":"operation-safe-1","state":"rolled_back","old_candidate_id":"candidate-old","new_candidate_id":"candidate-new","country":"JP","proxy_type":"datacenter","port":7928,"dns_verified":true,"exit_verified":true,"available":true,"error_code":"","expires_at":1700000180}}`)
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}

	if status, err := client.MainAssignment(context.Background()); err != nil || status.State != "idle" {
		t.Fatalf("idle status = %#v, err = %v", status, err)
	}
	staged, err := client.StageMainAssignment(context.Background(), MainAssignmentRequest{
		CandidateID:                "candidate-new",
		Country:                    "jp",
		ProxyType:                  "datacenter",
		ExpectedCurrentCandidateID: "candidate-old",
		IdempotencyKey:             "gateway-operation-1",
	})
	if err != nil || staged.State != "pending_commit" || !staged.DNSVerified || !staged.ExitVerified || !staged.Available {
		t.Fatalf("staged = %#v, err = %v", staged, err)
	}
	if committed, err := client.CommitMainAssignment(context.Background(), staged.OperationID); err != nil || committed.State != "committed" {
		t.Fatalf("committed = %#v, err = %v", committed, err)
	}
	if rolled, err := client.RollbackMainAssignment(context.Background(), staged.OperationID); err != nil || rolled.State != "rolled_back" {
		t.Fatalf("rolled = %#v, err = %v", rolled, err)
	}
}

func TestClientRejectsInvalidMainAssignmentInputAndResponse(t *testing.T) {
	client, err := NewClient("http://127.0.0.1:8790/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.StageMainAssignment(context.Background(), MainAssignmentRequest{CandidateID: "candidate"}); err == nil {
		t.Fatal("invalid stage input was accepted")
	}
	if _, err := client.CommitMainAssignment(context.Background(), "not.valid"); err == nil {
		t.Fatal("unsafe operation id was accepted")
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{"data":{"operation_id":"operation-safe-1","state":"pending_commit","old_candidate_id":"old","new_candidate_id":"new","country":"JP","proxy_type":"datacenter","port":7928,"dns_verified":true,"exit_verified":true,"available":true,"expires_at":1700000180,"config":"must-not-pass"}}`)
	}))
	t.Cleanup(server.Close)
	client, err = NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.MainAssignment(context.Background()); err == nil {
		t.Fatal("unknown assignment response field was accepted")
	}
}

func TestClientStagesAReplacementWhenTheCurrentMainIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/control/v1/main/assign" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var input MainAssignmentRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.ExpectedCurrentCandidateID != "" {
			t.Fatalf("expected current candidate = %q", input.ExpectedCurrentCandidateID)
		}
		fmt.Fprint(response, `{"data":{"operation_id":"operation-safe-1","state":"pending_commit","old_candidate_id":"","new_candidate_id":"candidate-new","country":"JP","proxy_type":"residential","port":7928,"dns_verified":true,"exit_verified":true,"available":true,"error_code":"","expires_at":1700000180}}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}

	staged, err := client.StageMainAssignment(context.Background(), MainAssignmentRequest{
		CandidateID: "candidate-new", Country: "JP", ProxyType: "residential",
		ExpectedCurrentCandidateID: "", IdempotencyKey: "gateway-recreate-main",
	})

	if err != nil || staged.State != "pending_commit" || staged.OldCandidateID != "" || staged.NewCandidateID != "candidate-new" {
		t.Fatalf("staged = %#v, err = %v", staged, err)
	}
}

func TestClientMainRepairUsesClosedPendingGatewayValidationContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/main/assign/operation-safe-1/repair-commit" {
				t.Fatalf("repair commit request = %s %s", request.Method, request.URL.Path)
			}
			fmt.Fprint(response, `{"data":{"operation_id":"operation-safe-1","state":"pending_gateway_validation","old_candidate_id":"candidate-old","new_candidate_id":"candidate-new","country":"JP","proxy_type":"datacenter","port":7928,"dns_verified":true,"exit_verified":true,"available":true,"error_code":"","resolution":"repair_commit","expires_at":1700000180}}`)
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/control/v1/main/assign/operation-safe-1/repair-replace" {
				t.Fatalf("repair replace request = %s %s", request.Method, request.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 3 || body["candidateId"] != "candidate-third" || body["country"] != "KR" || body["proxyType"] != "residential" {
				t.Fatalf("repair replace body = %#v", body)
			}
			fmt.Fprint(response, `{"data":{"operation_id":"operation-safe-1","state":"pending_gateway_validation","old_candidate_id":"candidate-old","new_candidate_id":"candidate-third","country":"KR","proxy_type":"residential","port":7928,"dns_verified":true,"exit_verified":true,"available":true,"error_code":"","resolution":"repair_replace","expires_at":1700000180}}`)
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}

	committed, err := client.RepairCommitMainAssignment(context.Background(), "operation-safe-1")
	if err != nil || committed.State != "pending_gateway_validation" {
		t.Fatalf("repair commit = %#v, err = %v", committed, err)
	}
	replaced, err := client.RepairReplaceMainAssignment(context.Background(), "operation-safe-1", MainRepairRequest{
		CandidateID: "candidate-third", Country: "kr", ProxyType: "residential",
	})
	if err != nil || replaced.State != "pending_gateway_validation" || replaced.NewCandidateID != "candidate-third" {
		t.Fatalf("repair replace = %#v, err = %v", replaced, err)
	}
}
