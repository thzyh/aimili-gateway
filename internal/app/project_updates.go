package app

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	"github.com/thzyh/aimili-gateway/internal/buildinfo"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

// This separate spool cannot be consumed by the older single-binary updater.
type projectUpdateManager struct {
	client *updatetxn.Client
	root   string
}

func newProjectUpdateManager() *projectUpdateManager {
	root := "/var/lib/aimili-gateway/project-update"
	c := &updatetxn.Client{RequestDir: filepath.Join(root, "requests"), ResultDir: filepath.Join(root, "results")}
	if runtime.GOOS != "windows" {
		uid := uint32(0)
		c.TrustedResultUID = &uid
	}
	return &projectUpdateManager{client: c, root: root}
}

func (m *projectUpdateManager) List(ctx context.Context) (httpapi.UpdateSummary, error) {
	summary := httpapi.UpdateSummary{Enabled: true, Project: true, CurrentGateway: buildinfo.Current().Version, Available: []httpapi.UpdateVersion{}}
	var current struct {
		Version string `json:"version"`
	}
	if updatetxn.ReadTrustedStateFile(filepath.Join(m.root, "current.json"), m.client.TrustedResultUID, &current) == nil {
		summary.CurrentGateway = current.Version
	}
	var catalog updateCatalog
	if updatetxn.ReadTrustedStateFile(filepath.Join(m.root, "catalog.json"), m.client.TrustedResultUID, &catalog) == nil && catalog.Capability && catalog.ExpiresAt.After(time.Now()) {
		summary.Available = catalog.Available
	}
	var lease struct {
		RunID   string `json:"runId"`
		Kind    string `json:"kind"`
		Version string `json:"version,omitempty"`
		Action  string `json:"action"`
		DryRun  bool   `json:"dryRun"`
	}
	if updatetxn.ReadTrustedStateFile(filepath.Join(m.client.RequestDir, ".update.lease"), nil, &lease) == nil {
		if result, err := m.client.Get(ctx, lease.RunID); err == nil && (!result.State.Terminal() || result.State == updatetxn.StateRepairRequired) {
			summary.ActiveRunID = lease.RunID
		}
	}
	return summary, nil
}

func (m *projectUpdateManager) Submit(ctx context.Context, r httpapi.UpdateRequest) (httpapi.UpdateResult, error) {
	if r.Kind != "project" || (r.Action != "check" && r.Action != "apply") {
		return httpapi.UpdateResult{}, httpapi.ErrUpdatesDisabled
	}
	if r.Action == "apply" {
		summary, _ := m.List(ctx)
		found := false
		for _, v := range summary.Available {
			if v.Kind == r.Kind && v.Version == r.Version && v.Compatible {
				found = true
			}
		}
		if !found {
			return httpapi.UpdateResult{}, httpapi.ErrUpdatesDisabled
		}
	}
	result, err := m.client.Submit(ctx, updatetxn.Request{RunID: r.RunID, Kind: updatetxn.KindProject, Action: updatetxn.Action(r.Action), Version: r.Version})
	out := publicUpdateResult(result)
	out.Action = r.Action
	return out, err
}

func (m *projectUpdateManager) Get(ctx context.Context, id string) (httpapi.UpdateResult, error) {
	result, err := m.client.Get(ctx, id)
	if err != nil {
		return httpapi.UpdateResult{}, err
	}
	out := publicUpdateResult(result)
	var progress httpapi.UpdateResult
	if updatetxn.ReadTrustedStateFile(filepath.Join(m.root, "progress", id+".json"), m.client.TrustedResultUID, &progress) == nil && progress.RunID == id && progress.Kind == "project" {
		out.Action = progress.Action
		if !result.State.Terminal() {
			return progress, nil
		}
	}
	if result.State == updatetxn.StateSuccess {
		out.Percent = 100
	}
	return out, nil
}
