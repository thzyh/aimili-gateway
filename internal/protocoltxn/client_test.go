package protocoltxn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestApplyWritesClosedEnvelopeAndReadsSafeResult(t *testing.T) {
	client, requests, results := newTestClient(t, time.Second)
	request := testRequest()
	done := make(chan struct{})
	go func() {
		defer close(done)
		path := waitForFile(t, filepath.Join(requests, request.OperationID+".apply.json"))
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			return
		}
		var envelope map[string]any
		if err := json.Unmarshal(contents, &envelope); err != nil {
			t.Error(err)
			return
		}
		if len(envelope) != 3 || envelope["action"] != "apply" || envelope["operationId"] != request.OperationID {
			t.Errorf("unsafe envelope: %#v", envelope)
			return
		}
		if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("request permissions are not private: %v %#o", err, info.Mode().Perm())
			return
		}
		writeResult(t, results, request.OperationID+".apply.json", `{"operationId":"operation-safe-1","status":"applied","errorCode":""}`)
	}()

	result, err := client.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if result.Status != "applied" || result.OperationID != request.OperationID || result.ErrorCode != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestDuplicateRequestIsIdempotentButDifferentRequestConflicts(t *testing.T) {
	client, requests, results := newTestClient(t, time.Second)
	request := testRequest()
	envelope := Envelope{Action: ActionApply, OperationID: request.OperationID, Request: &request}
	writeJSON(t, filepath.Join(requests, request.OperationID+".apply.json"), envelope)
	writeResult(t, results, request.OperationID+".apply.json", `{"operationId":"operation-safe-1","status":"applied","errorCode":""}`)
	if _, err := client.Apply(context.Background(), request); err != nil {
		t.Fatalf("identical replay failed: %v", err)
	}

	request.NewMode = "hysteria2_quic_tls"
	if _, err := client.Apply(context.Background(), request); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("different replay error = %v", err)
	}
}

func TestRejectsMalformedOrSecretBearingResult(t *testing.T) {
	client, _, results := newTestClient(t, time.Second)
	request := testRequest()
	writeResult(t, results, request.OperationID+".apply.json", `{"operationId":"operation-safe-1","status":"failed","errorCode":"operation_failed","uuid":"must-not-pass"}`)
	if _, err := client.Apply(context.Background(), request); !errors.Is(err, ErrUnsafeResult) {
		t.Fatalf("unknown result field error = %v", err)
	}
}

func TestTimeoutAndCancellationDoNotExposeRequestContents(t *testing.T) {
	client, _, _ := newTestClient(t, 20*time.Millisecond)
	request := testRequest()
	if _, err := client.Apply(context.Background(), request); !errors.Is(err, ErrTimeout) || err.Error() != "protocol transaction timed out" {
		t.Fatalf("timeout error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Finalize(ctx, "operation-safe-2"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestRenewWritesClosedEnvelopeAndAcceptsOnlyRenewedStatus(t *testing.T) {
	client, requests, results := newTestClient(t, time.Second)
	operationID := "operation-safe-4"
	done := make(chan struct{})
	go func() {
		defer close(done)
		path := waitForFile(t, filepath.Join(requests, operationID+".renew.json"))
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			return
		}
		var envelope map[string]any
		if err := json.Unmarshal(contents, &envelope); err != nil {
			t.Error(err)
			return
		}
		heartbeatID, _ := envelope["heartbeatId"].(string)
		if len(envelope) != 3 || envelope["action"] != "renew" || envelope["operationId"] != operationID || len(heartbeatID) != 32 {
			t.Errorf("unsafe renew envelope: %#v", envelope)
			return
		}
		writeResult(t, results, operationID+".renew.json", fmt.Sprintf(`{"operationId":"operation-safe-4","heartbeatId":%q,"status":"renewed","errorCode":""}`, heartbeatID))
	}()

	result, err := client.Renew(context.Background(), operationID)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if result.Status != "renewed" || result.OperationID != operationID {
		t.Fatalf("unexpected renew result: %#v", result)
	}
}

func TestConsecutiveRenewCorrelatesSecondHelperFailure(t *testing.T) {
	client, requests, results := newTestClient(t, time.Second)
	operationID := "operation-safe-5"
	serve := func(status, errorCode string) <-chan string {
		heartbeat := make(chan string, 1)
		go func() {
			path := waitForFile(t, filepath.Join(requests, operationID+".renew.json"))
			var envelope map[string]any
			contents, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(contents, &envelope) != nil {
				t.Errorf("read renew envelope: %v", err)
				return
			}
			heartbeatID, _ := envelope["heartbeatId"].(string)
			if !safeHeartbeatID.MatchString(heartbeatID) {
				t.Errorf("invalid renew heartbeat id: %q", heartbeatID)
				return
			}
			if err := os.Remove(path); err != nil {
				t.Errorf("remove renew request: %v", err)
				return
			}
			writeResult(t, results, operationID+".renew.json", fmt.Sprintf(
				`{"operationId":%q,"heartbeatId":%q,"status":%q,"errorCode":%q}`,
				operationID, heartbeatID, status, errorCode,
			))
			heartbeat <- heartbeatID
		}()
		return heartbeat
	}

	firstHeartbeat := serve("renewed", "")
	first, err := client.Renew(context.Background(), operationID)
	if err != nil || first.Status != "renewed" {
		t.Fatalf("first renew = %#v, err = %v", first, err)
	}
	firstID := <-firstHeartbeat
	if err := os.Remove(filepath.Join(results, operationID+".renew.json")); err != nil {
		t.Fatal(err)
	}
	secondHeartbeat := serve("failed", "operation_not_applied")
	second, err := client.Renew(context.Background(), operationID)
	if err != nil || second.Status != "failed" || second.ErrorCode != "operation_not_applied" {
		t.Fatalf("second renew = %#v, err = %v", second, err)
	}
	secondID := <-secondHeartbeat
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("renew heartbeat ids = %q, %q", firstID, secondID)
	}
}

func TestRenewAfterRestartIgnoresLegacySuccessResult(t *testing.T) {
	client, _, results := newTestClient(t, 20*time.Millisecond)
	operationID := "operation-safe-6"
	writeResult(t, results, operationID+".renew.json", `{"operationId":"operation-safe-6","status":"renewed","errorCode":""}`)

	result, err := client.Renew(context.Background(), operationID)

	if !errors.Is(err, ErrTimeout) || result != (Result{}) {
		t.Fatalf("legacy result was reused: result=%#v err=%v", result, err)
	}
}

func TestReadResultWaitsForMatchingRenewHeartbeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation-safe-7.renew.json")
	if err := os.WriteFile(path, []byte(`{"operationId":"operation-safe-7","heartbeatId":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","status":"renewed","errorCode":""}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, ready, err := readResult(path, Envelope{
		Action: ActionRenew, OperationID: "operation-safe-7", HeartbeatID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})

	if err != nil || ready || result != (Result{}) {
		t.Fatalf("mismatched heartbeat result=%#v ready=%t err=%v", result, ready, err)
	}
}

func TestRejectsInvalidOperationAndSymlinkedSpool(t *testing.T) {
	client, _, _ := newTestClient(t, time.Second)
	request := testRequest()
	request.OperationID = "../escape"
	if _, err := client.Apply(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid operation error = %v", err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows test accounts may not create symlinks")
	}
	root := t.TempDir()
	realRequests := filepath.Join(root, "real-requests")
	results := filepath.Join(root, "results")
	if err := os.MkdirAll(realRequests, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(results, 0o700); err != nil {
		t.Fatal(err)
	}
	requests := filepath.Join(root, "requests")
	if err := os.Symlink(realRequests, requests); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{RequestDir: requests, ResultDir: results, Timeout: time.Second}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlinked spool error = %v", err)
	}
}

func TestCreateOrMatchPublishesCompleteFileAtomically(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "operation-safe-1.apply.json")
	contents := []byte(`{"payload":"` + strings.Repeat("x", 8<<20) + `"}`)
	done := make(chan error, 1)
	go func() { done <- createOrMatch(path, contents) }()
	for {
		published, err := os.ReadFile(path)
		if err == nil && len(published) != len(contents) {
			t.Fatalf("partially written request became visible: %d of %d bytes", len(published), len(contents))
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			published, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(published, contents) {
				t.Fatalf("published request mismatch: %v", err)
			}
			return
		default:
		}
	}
}

func newTestClient(t *testing.T, timeout time.Duration) (*Client, string, string) {
	t.Helper()
	root := t.TempDir()
	requests := filepath.Join(root, "requests")
	results := filepath.Join(root, "results")
	for _, path := range []string{requests, results} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	client, err := New(Config{RequestDir: requests, ResultDir: results, Timeout: timeout, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return client, requests, results
}

func testRequest() Request {
	return Request{
		OperationID:         "operation-safe-1",
		EgressID:            "agw-main",
		InboundID:           7,
		InboundTag:          "aimili-reality",
		Port:                8443,
		OldMode:             "vless_tcp_reality_vision",
		NewMode:             "vless_xhttp_reality",
		ExpectedFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("request file was not created: %s", path)
	return path
}

func writeResult(t *testing.T, directory, name, body string) {
	t.Helper()
	path := filepath.Join(directory, name)
	file, err := os.CreateTemp(directory, "."+name+".*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if runtime.GOOS != "windows" {
		_ = file.Chmod(0o600)
	}
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
