package protocoltxn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

var (
	ErrInvalidRequest    = errors.New("invalid protocol transaction request")
	ErrOperationConflict = errors.New("protocol transaction operation conflict")
	ErrUnsafePath        = errors.New("unsafe protocol transaction path")
	ErrUnsafeResult      = errors.New("unsafe protocol transaction result")
	ErrTimeout           = errors.New("protocol transaction timed out")
)

var (
	safeOperationID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	safeHeartbeatID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	safeEgressID    = regexp.MustCompile(`^agw-[a-z0-9][a-z0-9_-]{0,95}$`)
	safeHash        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	safeErrorCode   = regexp.MustCompile(`^[a-z0-9_]{0,64}$`)
)

type Action string

const (
	ActionApply    Action = "apply"
	ActionRenew    Action = "renew"
	ActionFinalize Action = "finalize"
	ActionRollback Action = "rollback"
)

type Config struct {
	RequestDir   string
	ResultDir    string
	Timeout      time.Duration
	PollInterval time.Duration
}

type Request struct {
	OperationID         string `json:"operationId"`
	EgressID            string `json:"egressId"`
	InboundID           int64  `json:"inboundId"`
	InboundTag          string `json:"inboundTag"`
	Port                int    `json:"port"`
	OldMode             string `json:"oldMode"`
	NewMode             string `json:"newMode"`
	ExpectedFingerprint string `json:"expectedFingerprint"`
}

type Envelope struct {
	Action      Action   `json:"action"`
	OperationID string   `json:"operationId"`
	HeartbeatID string   `json:"heartbeatId,omitempty"`
	Request     *Request `json:"request,omitempty"`
}

type Result struct {
	OperationID string `json:"operationId"`
	HeartbeatID string `json:"heartbeatId,omitempty"`
	Status      string `json:"status"`
	ErrorCode   string `json:"errorCode"`
}

type Client struct {
	requestDir   string
	resultDir    string
	timeout      time.Duration
	pollInterval time.Duration
}

func New(config Config) (*Client, error) {
	if config.Timeout <= 0 || config.PollInterval <= 0 || config.PollInterval > config.Timeout {
		return nil, ErrInvalidRequest
	}
	requestDir, err := safeDirectory(config.RequestDir)
	if err != nil {
		return nil, err
	}
	resultDir, err := safeDirectory(config.ResultDir)
	if err != nil {
		return nil, err
	}
	if requestDir == resultDir || filepath.Base(requestDir) != "requests" || filepath.Base(resultDir) != "results" || filepath.Dir(requestDir) != filepath.Dir(resultDir) {
		return nil, ErrUnsafePath
	}
	return &Client{requestDir: requestDir, resultDir: resultDir, timeout: config.Timeout, pollInterval: config.PollInterval}, nil
}

func (c *Client) Apply(ctx context.Context, request Request) (Result, error) {
	if !validRequest(request) {
		return Result{}, ErrInvalidRequest
	}
	return c.execute(ctx, Envelope{Action: ActionApply, OperationID: request.OperationID, Request: &request})
}

func (c *Client) Finalize(ctx context.Context, operationID string) (Result, error) {
	return c.simple(ctx, ActionFinalize, operationID)
}

func (c *Client) Renew(ctx context.Context, operationID string) (Result, error) {
	if !safeOperationID.MatchString(operationID) {
		return Result{}, ErrInvalidRequest
	}
	heartbeatBytes := make([]byte, 16)
	if _, err := rand.Read(heartbeatBytes); err != nil {
		return Result{}, fmt.Errorf("generate protocol transaction heartbeat: %w", err)
	}
	return c.execute(ctx, Envelope{
		Action:      ActionRenew,
		OperationID: operationID,
		HeartbeatID: hex.EncodeToString(heartbeatBytes),
	})
}

func (c *Client) Rollback(ctx context.Context, operationID string) (Result, error) {
	return c.simple(ctx, ActionRollback, operationID)
}

func (c *Client) simple(ctx context.Context, action Action, operationID string) (Result, error) {
	if !safeOperationID.MatchString(operationID) {
		return Result{}, ErrInvalidRequest
	}
	return c.execute(ctx, Envelope{Action: action, OperationID: operationID})
}

func (c *Client) execute(ctx context.Context, envelope Envelope) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	name := envelope.OperationID + "." + string(envelope.Action) + ".json"
	requestPath := filepath.Join(c.requestDir, name)
	resultPath := filepath.Join(c.resultDir, name)
	contents, err := json.Marshal(envelope)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	if err := createOrMatch(requestPath, contents); err != nil {
		return Result{}, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		result, ready, err := readResult(resultPath, envelope)
		if err != nil {
			return Result{}, err
		}
		if ready {
			return result, nil
		}
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return Result{}, ErrTimeout
			}
			return Result{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func validRequest(request Request) bool {
	if !safeOperationID.MatchString(request.OperationID) || !safeEgressID.MatchString(request.EgressID) || request.InboundID < 1 || request.Port < 1 || request.Port > 65535 || !safeHash.MatchString(request.ExpectedFingerprint) {
		return false
	}
	validMode := func(value string) bool {
		switch value {
		case "vless_tcp_reality_vision", "vless_xhttp_reality", "hysteria2_quic_tls":
			return true
		default:
			return false
		}
	}
	return request.InboundTag != "" && validMode(request.OldMode) && validMode(request.NewMode) && request.OldMode != request.NewMode
}

func safeDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrUnsafePath
	}
	clean := filepath.Clean(path)
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrUnsafePath
	}
	return clean, nil
}

func createOrMatch(path string, contents []byte) error {
	if err := matchExisting(path, contents); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create protocol transaction request: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if runtime.GOOS != "windows" {
		_ = file.Chmod(0o600)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write protocol transaction request: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync protocol transaction request: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close protocol transaction request: %w", err)
	}
	if err := os.Link(temporary, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return matchExisting(path, contents)
		}
		return fmt.Errorf("publish protocol transaction request: %w", err)
	}
	return nil
}

func matchExisting(path string, contents []byte) error {
	info, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16<<10 {
		return ErrUnsafePath
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(existing, contents) {
		return ErrOperationConflict
	}
	return nil
}

func readResult(path string, envelope Envelope) (Result, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 4096 {
		return Result{}, false, ErrUnsafeResult
	}
	file, err := os.Open(path)
	if err != nil {
		return Result{}, false, ErrUnsafeResult
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, false, ErrUnsafeResult
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Result{}, false, ErrUnsafeResult
	}
	if result.OperationID != envelope.OperationID || !safeErrorCode.MatchString(result.ErrorCode) || !validStatus(envelope.Action, result.Status) {
		return Result{}, false, ErrUnsafeResult
	}
	if envelope.Action == ActionRenew {
		if !safeHeartbeatID.MatchString(envelope.HeartbeatID) {
			return Result{}, false, ErrUnsafeResult
		}
		if result.HeartbeatID != envelope.HeartbeatID {
			return Result{}, false, nil
		}
	} else if envelope.HeartbeatID != "" || result.HeartbeatID != "" {
		return Result{}, false, ErrUnsafeResult
	}
	return result, true, nil
}

func validStatus(action Action, status string) bool {
	switch action {
	case ActionApply:
		return status == "applied" || status == "failed" || status == "repair_required"
	case ActionRenew:
		return status == "renewed" || status == "failed" || status == "repair_required"
	case ActionFinalize:
		return status == "finalized" || status == "failed" || status == "repair_required"
	case ActionRollback:
		return status == "rolled_back" || status == "failed" || status == "repair_required"
	default:
		return false
	}
}
