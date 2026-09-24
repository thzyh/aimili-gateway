package app

import (
	"context"
	"encoding/json"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectCheckDoesNotInstallAndRestoresProgress(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"requests", "results", "progress"} {
		if err := os.Mkdir(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := &projectUpdateManager{root: root, client: &updatetxn.Client{RequestDir: filepath.Join(root, "requests"), ResultDir: filepath.Join(root, "results")}}
	ctx := context.Background()
	id := strings.Repeat("a", 64)
	result, err := m.Submit(ctx, httpapi.UpdateRequest{RunID: id, Kind: "project", Action: "check"})
	if err != nil || result.Action != "check" {
		t.Fatalf("%+v %v", result, err)
	}
	summary, _ := m.List(ctx)
	if summary.ActiveRunID != id {
		t.Fatal("active transaction missing")
	}
	write := func(path string, value any) {
		t.Helper()
		b, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(root, path), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("progress/"+id+".json", httpapi.UpdateResult{RunID: id, Kind: "project", Action: "check", State: "downloading", Phase: "discover", Percent: 2})
	progress, err := m.Get(ctx, id)
	if err != nil || progress.Phase != "discover" {
		t.Fatalf("%+v %v", progress, err)
	}
	write("results/"+id+".json", updatetxn.Result{RunID: id, Kind: updatetxn.KindProject, State: updatetxn.StateSuccess})
	final, err := m.Get(ctx, id)
	if err != nil || final.State != "success" || final.Action != "check" {
		t.Fatalf("%+v %v", final, err)
	}
	if _, err = m.Submit(ctx, httpapi.UpdateRequest{RunID: strings.Repeat("b", 64), Kind: "project", Action: "apply", Version: "v0.2.12-vps"}); err != httpapi.ErrUpdatesDisabled {
		t.Fatal("unsigned/expired catalog permitted install")
	}
	write("catalog.json", updateCatalog{Capability: true, ExpiresAt: time.Now().Add(time.Hour), Available: []httpapi.UpdateVersion{{Kind: "project", Version: "v0.2.12-vps", Compatible: true}}})
	if _, err = m.Submit(ctx, httpapi.UpdateRequest{RunID: strings.Repeat("b", 64), Kind: "project", Action: "apply", Version: "v0.2.12-vps"}); err != nil {
		t.Fatal(err)
	}
}
