package buildinfo

import (
	"runtime"
	"testing"
)

func TestCurrentReportsLinkerMetadataAndRuntimePlatform(t *testing.T) {
	originalVersion, originalCommit, originalBuiltAt := Version, Commit, BuiltAt
	t.Cleanup(func() {
		Version, Commit, BuiltAt = originalVersion, originalCommit, originalBuiltAt
	})
	Version, Commit, BuiltAt = "v1.2.3", "abc1234", "2026-09-05T00:00:00Z"

	got := Current()
	if got.Version != "v1.2.3" || got.Commit != "abc1234" || got.BuiltAt != "2026-09-05T00:00:00Z" {
		t.Fatalf("build metadata = %#v", got)
	}
	if got.APIVersion != "v1" || got.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		t.Fatalf("runtime identity = %#v", got)
	}
}
