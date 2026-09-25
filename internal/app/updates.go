package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/buildinfo"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"github.com/thzyh/aimili-gateway/internal/releaseverify"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

type updateManager struct {
	client      *updatetxn.Client
	uiRoot      string
	catalogPath string
}

func newUpdateManager(cfg config.Config) httpapi.UpdateManager {
	if cfg.ProjectUpdateEnabled {
		return newProjectUpdateManager()
	}
	if !cfg.UpdateEnabled || cfg.UpdateRequestDir == "" || cfg.UpdateResultDir == "" {
		return nil
	}
	client := &updatetxn.Client{RequestDir: cfg.UpdateRequestDir, ResultDir: cfg.UpdateResultDir}
	if runtime.GOOS != "windows" {
		trustedResultUID := uint32(0)
		client.TrustedResultUID = &trustedResultUID
	}
	manager := &updateManager{client: client, uiRoot: cfg.ExternalUIRoot, catalogPath: cfg.UpdateCatalogFile}
	return manager
}

type updateCatalog struct {
	Capability bool                    `json:"capability"`
	ExpiresAt  time.Time               `json:"expiresAt"`
	Available  []httpapi.UpdateVersion `json:"available"`
}

func (m *updateManager) catalog() (updateCatalog, error) {
	var catalog updateCatalog
	if err := updatetxn.ReadTrustedStateFile(m.catalogPath, m.client.TrustedResultUID, &catalog); err != nil {
		return catalog, httpapi.ErrUpdatesDisabled
	}
	if !catalog.Capability || !catalog.ExpiresAt.After(time.Now()) || len(catalog.Available) == 0 || len(catalog.Available) > 64 {
		return catalog, httpapi.ErrUpdatesDisabled
	}
	for _, v := range catalog.Available {
		if updatetxn.ValidateRequest(updatetxn.Request{RunID: strings.Repeat("a", 64), Kind: updatetxn.Kind(v.Kind), Version: v.Version, Action: updatetxn.ActionApply}) != nil {
			return catalog, httpapi.ErrUpdatesDisabled
		}
	}
	return catalog, nil
}

func (m *updateManager) List(context.Context) (httpapi.UpdateSummary, error) {
	summary := httpapi.UpdateSummary{Enabled: true, CurrentGateway: buildinfo.Current().Version, Available: []httpapi.UpdateVersion{}}
	if catalog, err := m.catalog(); err == nil {
		summary.Available = catalog.Available
	}
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
	if request.Action == "apply" {
		if request.Kind == "gateway" {
			comparison, err := releaseverify.CompareVersions(request.Version, buildinfo.Current().Version)
			if err != nil || comparison <= 0 {
				return httpapi.UpdateResult{}, httpapi.ErrUpdatesDisabled
			}
		} else {
			catalog, err := m.catalog()
			if err != nil {
				return httpapi.UpdateResult{}, err
			}
			found := false
			for _, v := range catalog.Available {
				if v.Kind == request.Kind && v.Version == request.Version && v.Compatible {
					found = true
				}
			}
			if !found {
				return httpapi.UpdateResult{}, httpapi.ErrUpdatesDisabled
			}
		}
	}
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
	path := result.UpgradePath
	if !strings.HasPrefix(path, "https://github.com/thzyh/aimili-gateway/blob/main/docs/upgrade.md#") || len(path) > 180 {
		path = ""
	}
	return httpapi.UpdateResult{RunID: result.RunID, Kind: string(result.Kind), Version: result.Version, State: string(result.State), ErrorCode: code, UpgradePath: path}
}

func safePublicUpdateError(code string) bool {
	if len(code) < 1 || len(code) > 64 {
		return false
	}
	return strings.IndexFunc(code, func(character rune) bool {
		return character != '_' && (character < 'a' || character > 'z')
	}) == -1
}
