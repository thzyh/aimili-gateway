package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/buildinfo"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

type updateManager struct {
	client *updatetxn.Client
	uiRoot string
}

func newUpdateManager(requestDir, resultDir, uiRoot string) httpapi.UpdateManager {
	if requestDir == "" || resultDir == "" {
		return nil
	}
	client := &updatetxn.Client{RequestDir: requestDir, ResultDir: resultDir}
	if runtime.GOOS != "windows" {
		trustedResultUID := uint32(0)
		client.TrustedResultUID = &trustedResultUID
	}
	return &updateManager{client: client, uiRoot: uiRoot}
}

func (m *updateManager) List(context.Context) (httpapi.UpdateSummary, error) {
	summary := httpapi.UpdateSummary{CurrentGateway: buildinfo.Current().Version, Available: []httpapi.UpdateVersion{}}
	if m.uiRoot == "" {
		return summary, nil
	}
	target, err := filepath.EvalSymlinks(filepath.Join(m.uiRoot, "current"))
	if err == nil {
		summary.CurrentUI = filepath.Base(target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return httpapi.UpdateSummary{}, err
	}
	return summary, nil
}

func (m *updateManager) Submit(ctx context.Context, request httpapi.UpdateRequest) (httpapi.UpdateResult, error) {
	transaction := updatetxn.Request{
		RunID: request.RunID, Kind: updatetxn.Kind(request.Kind), Version: request.Version, Action: updatetxn.Action(request.Action),
	}
	result, err := m.client.Submit(ctx, transaction)
	return publicUpdateResult(result), err
}

func (m *updateManager) Get(ctx context.Context, runID string) (httpapi.UpdateResult, error) {
	result, err := m.client.Get(ctx, runID)
	return publicUpdateResult(result), err
}

func publicUpdateResult(result updatetxn.Result) httpapi.UpdateResult {
	code := result.ErrorCode
	if code != "" && !safePublicUpdateError(code) {
		code = "operation_failed"
	}
	return httpapi.UpdateResult{RunID: result.RunID, Kind: string(result.Kind), Version: result.Version, State: string(result.State), ErrorCode: code}
}

func safePublicUpdateError(code string) bool {
	if len(code) < 1 || len(code) > 64 {
		return false
	}
	return strings.IndexFunc(code, func(character rune) bool {
		return character != '_' && (character < 'a' || character > 'z')
	}) == -1
}
