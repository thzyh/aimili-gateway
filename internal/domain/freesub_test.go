package domain

import "testing"

func testFreesubConnection() FreesubBackupConnection {
	return FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-123", CountryCode: "IN",
		Protocol: "vless", Status: FreesubBackupDegraded, Version: 1,
		CandidateConfig: []byte(`{"type":"vless"}`),
	}
}

func TestFreesubBackupReplacementIsSingleUse(t *testing.T) {
	connection := testFreesubConnection()
	if err := connection.BeginAutoReplacement("failure-1"); err != nil {
		t.Fatalf("first replacement: %v", err)
	}
	if connection.RepairAttempts != 1 || connection.Status != FreesubBackupProvisioning {
		t.Fatalf("replacement state = %#v", connection)
	}
	if err := connection.BeginAutoReplacement("failure-2"); err == nil {
		t.Fatal("second automatic replacement unexpectedly allowed")
	}
}

func TestFreesubBackupKeepsCardAfterFailure(t *testing.T) {
	connection := testFreesubConnection()
	if err := connection.Transition(FreesubBackupWaitingManual); err != nil {
		t.Fatalf("waiting-manual transition: %v", err)
	}
	if connection.Status != FreesubBackupWaitingManual {
		t.Fatalf("connection status = %s", connection.Status)
	}
}
