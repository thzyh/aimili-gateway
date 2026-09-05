package gatewayupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func TestRootRequestMustMatchSignedManifest(t *testing.T) {
	for _, field := range []string{"version", "kind"} {
		t.Run(field, func(t *testing.T) {
			cfg, runner := gatewayFixture(t, "control-plane-only")
			if field == "version" {
				cfg.Request.Version = "v1.2.4"
			} else {
				cfg.Request.Kind = updatetxn.KindUI
			}
			_, err := Install(context.Background(), cfg)
			if ErrorCode(err) != "request_manifest_mismatch" || len(runner.calls) != 0 {
				t.Fatalf("root accepted different signed request: %v calls=%v", err, runner.calls)
			}
		})
	}
}

func TestInterruptedSwitchRecoversWithoutSecondApply(t *testing.T) {
	for _, phase := range []string{"before_stop", "after_stop", "after_replace", "after_start", "before_result"} {
		t.Run(phase, func(t *testing.T) {
			cfg, _ := gatewayFixture(t, "control-plane-only")
			crash := errors.New("injected interruption")
			cfg.Fault = func(actual string) error {
				if actual != phase {
					return nil
				}
				body, err := os.ReadFile(filepath.Join(cfg.StateDir, "journal", "active.json"))
				if err != nil {
					t.Fatal("missing durable journal before mutation", err)
				}
				var journal map[string]any
				if json.Unmarshal(body, &journal) != nil || journal["oldDigest"] == "" || journal["newDigest"] == "" || journal["baseline"] == "" {
					t.Fatal("incomplete journal")
				}
				return crash
			}
			if _, err := Install(context.Background(), cfg); !errors.Is(err, crash) {
				t.Fatalf("fault %s not exposed: %v", phase, err)
			}
			cfg.Fault = nil
			cfg.ShadowCheck = func(context.Context, []byte, releaseverify.GatewayManifest) error {
				t.Fatal("recovery replayed shadow/apply")
				return nil
			}
			result, err := Install(context.Background(), cfg)
			if err != nil || !result.State.Terminal() || result.State == updatetxn.StateRepairRequired {
				t.Fatalf("recovery failed: %+v %v", result, err)
			}
			if got := string(mustRead(t, cfg.PreviousPath)); got != "old gateway" {
				t.Fatalf("recovery overwrote unique previous: %q", got)
			}
			if _, err := os.Stat(cfg.StagingDir); err != nil {
				t.Fatal("staging deleted before durable terminal result")
			}
		})
	}
}
