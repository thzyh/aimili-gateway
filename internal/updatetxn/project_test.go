package updatetxn

import (
	"strings"
	"testing"
)

func TestProjectDiscoveryAndPinnedApply(t *testing.T) {
	for _, tc := range []struct {
		action, version string
		valid           bool
	}{
		{"check", "", true}, {"apply", "v0.2.12-vps", true},
		{"apply", "latest", false}, {"apply", "v01.2.3-vps", false},
		{"check", "v0.2.12-vps", false}, {"rollback", "", false},
	} {
		err := ValidateRequest(Request{RunID: strings.Repeat("a", 64), Kind: Kind("project"), Action: Action(tc.action), Version: tc.version})
		if (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
