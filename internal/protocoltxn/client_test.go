package protocoltxn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
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
