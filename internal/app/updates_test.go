package app

import (
	"runtime"
	"testing"
)

func TestUpdateManagerTrustedResultUIDFollowsPlatformOwnerSemantics(t *testing.T) {
	manager := newUpdateManager("requests", "results", "").(*updateManager)
	if runtime.GOOS == "windows" {
		if manager.client.TrustedResultUID != nil {
			t.Fatalf("Windows TrustedResultUID = %v, want nil", *manager.client.TrustedResultUID)
		}
		return
	}
	if manager.client.TrustedResultUID == nil || *manager.client.TrustedResultUID != 0 {
		t.Fatalf("TrustedResultUID = %v, want root UID 0", manager.client.TrustedResultUID)
	}
}
