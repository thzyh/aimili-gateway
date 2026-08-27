package accountsync

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type fakeAccountStore struct {
	mu          sync.Mutex
	admin       store.Admin
	credentials store.UnifiedCredentials
	state       store.AccountSyncState
	commitErr   error
	commits     []store.UnifiedCredentialUpdate
	states      []store.AccountSyncState
	calls       *[]string
}

func (fake *fakeAccountStore) GetAdmin(context.Context) (store.Admin, error) {
	return fake.admin, nil
}

func (fake *fakeAccountStore) LoadUnifiedCredentials(context.Context, []byte) (store.UnifiedCredentials, error) {
	return fake.credentials, nil
}

func (fake *fakeAccountStore) GetAccountSyncState(context.Context) (store.AccountSyncState, error) {
	return fake.state, nil
}

func (fake *fakeAccountStore) SetAccountSyncState(_ context.Context, state store.AccountSyncState) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.state = state
	fake.states = append(fake.states, state)
	return nil
}

func (fake *fakeAccountStore) CommitUnifiedCredentialsAndRevokeSessions(_ context.Context, update store.UnifiedCredentialUpdate) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	*fake.calls = append(*fake.calls, "store.commit")
	if fake.commitErr != nil {
		return fake.commitErr
	}
	fake.commits = append(fake.commits, update)
	fake.admin.Username = update.Username
	fake.admin.PasswordHash = update.PasswordHash
	fake.admin.SecurityUpdatedAt = update.UpdatedAt
	fake.credentials = store.UnifiedCredentials{Username: update.Username, Password: []byte("new-password-marker")}
	fake.state = store.AccountSyncState{Status: update.Status, UsernameFingerprint: update.UsernameFingerprint, LastCheckedAt: update.UpdatedAt}
	return nil
}

type fakeAimiliAdmin struct {
	calls          *[]string
	username       string
	password       string
	updateFailures map[string]error
	verifyErr      error
}

func (fake *fakeAimiliAdmin) Capabilities(context.Context) (aimili.Capabilities, error) {
	*fake.calls = append(*fake.calls, "aimili.capabilities")
	return aimili.Capabilities{APIVersion: "v1", Capabilities: []string{"admin.read", "admin.verify", "admin.update", "admin.sessions.issue"}}, nil
}

func (fake *fakeAimiliAdmin) AdminStatus(context.Context) (aimili.AdminStatus, error) {
	*fake.calls = append(*fake.calls, "aimili.status")
	return aimili.AdminStatus{Username: fake.username}, nil
}

func (fake *fakeAimiliAdmin) VerifyAdmin(_ context.Context, input aimili.AdminUpdate) error {
	*fake.calls = append(*fake.calls, "aimili.verify:"+input.Username)
	if fake.verifyErr != nil {
		return fake.verifyErr
	}
	if input.Username != fake.username || string(input.Password) != fake.password {
		return &aimili.AdapterError{Code: "credentials_rejected"}
	}
	return nil
}

func (fake *fakeAimiliAdmin) UpdateAdmin(_ context.Context, input aimili.AdminUpdate) error {
	*fake.calls = append(*fake.calls, "aimili.update:"+input.Username)
	if err := fake.updateFailures[input.Username]; err != nil {
		return err
	}
	fake.username = input.Username
	fake.password = string(input.Password)
	return nil
}

type fakeXUIAdmin struct {
	calls          *[]string
	username       string
	password       string
	updateFailures map[string]error
	capabilities   xui.AdminCapabilities
}

func (fake *fakeXUIAdmin) ProbeAdminCapabilities(context.Context) (xui.AdminCapabilities, error) {
	*fake.calls = append(*fake.calls, "xui.capabilities")
	return fake.capabilities, nil
}

func (fake *fakeXUIAdmin) VerifyAdmin(_ context.Context, credentials xui.Credentials) error {
	*fake.calls = append(*fake.calls, "xui.verify:"+credentials.Username)
	if credentials.Username != fake.username || credentials.Password != fake.password {
		return &xui.AdapterError{Code: "credentials_rejected"}
	}
	return nil
}

func (fake *fakeXUIAdmin) UpdateAdmin(_ context.Context, current, next xui.Credentials) error {
	*fake.calls = append(*fake.calls, "xui.update:"+next.Username)
	if err := fake.updateFailures[next.Username]; err != nil {
		return err
	}
	if current.Username != fake.username || current.Password != fake.password {
		return &xui.AdapterError{Code: "credentials_rejected"}
	}
	fake.username = next.Username
	fake.password = next.Password
	return nil
}

type coordinatorFixture struct {
	coordinator *Coordinator
	store       *fakeAccountStore
	aimili      *fakeAimiliAdmin
	xui         *fakeXUIAdmin
	calls       *[]string
}

func newCoordinatorFixture(t *testing.T) coordinatorFixture {
	t.Helper()
	calls := make([]string, 0)
	now := time.Unix(1_700_000_000, 0).UTC()
	database := &fakeAccountStore{
		admin:       store.Admin{Username: "owner", PasswordHash: []byte("old-hash"), SecurityUpdatedAt: now},
		credentials: store.UnifiedCredentials{Username: "owner", Password: []byte("old-password-marker")},
		state:       store.AccountSyncState{Status: store.AccountSyncSynced},
		calls:       &calls,
	}
	aimiliAdmin := &fakeAimiliAdmin{calls: &calls, username: "owner", password: "old-password-marker", updateFailures: map[string]error{}}
	xuiAdmin := &fakeXUIAdmin{
		calls: &calls, username: "owner", password: "old-password-marker", updateFailures: map[string]error{},
		capabilities: xui.AdminCapabilities{ContractVersion: "v3-update-user", CanUpdate: true, CanBridge: true, TOTPCompatible: true},
	}
	coordinator, err := New(database, bytesOf(0x42, 32), aimiliAdmin, xuiAdmin, func() time.Time { return now.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	coordinator.hashPassword = func([]byte) (string, error) { return "new-hash", nil }
	return coordinatorFixture{coordinator: coordinator, store: database, aimili: aimiliAdmin, xui: xuiAdmin, calls: &calls}
}

func TestChangePreflightFailureHasNoSideEffects(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.aimili.verifyErr = &aimili.AdapterError{Code: "credentials_rejected"}
	err := fixture.coordinator.Change(context.Background(), ChangeRequest{Username: "renamed", Password: []byte("new-password-marker")})
	assertCoordinatorCode(t, err, "account_drift")
	for _, call := range *fixture.calls {
		if strings.Contains(call, "update") || call == "store.commit" {
			t.Fatalf("preflight changed state: %#v", *fixture.calls)
		}
	}
}

func TestChangeUpdatesBothServicesBeforeAtomicLocalCommit(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	err := fixture.coordinator.Change(context.Background(), ChangeRequest{Username: "renamed", Password: []byte("new-password-marker")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"aimili.capabilities", "xui.capabilities", "aimili.status", "aimili.verify:owner", "xui.verify:owner",
		"aimili.update:renamed", "xui.update:renamed", "aimili.verify:renamed", "xui.verify:renamed",
		"store.commit", "aimili.status", "aimili.verify:renamed", "xui.verify:renamed",
	}
	if strings.Join(*fixture.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("call order:\n got %#v\nwant %#v", *fixture.calls, want)
	}
	if len(fixture.store.commits) != 1 || fixture.store.commits[0].Status != store.AccountSyncSynced || fixture.store.commits[0].UsernameFingerprint == "" {
		t.Fatalf("local commits = %#v", fixture.store.commits)
	}
}

func TestChangeRollsBackAimiliWhenXUIUpdateFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.xui.updateFailures["renamed"] = errors.New("xui unavailable")
	err := fixture.coordinator.Change(context.Background(), ChangeRequest{Username: "renamed", Password: []byte("new-password-marker")})
	assertCoordinatorCode(t, err, "service_unavailable")
	joined := strings.Join(*fixture.calls, "|")
	if !strings.Contains(joined, "aimili.update:renamed|xui.update:renamed|xui.update:owner|xui.verify:owner|aimili.update:owner") {
		t.Fatalf("missing reverse rollback: %#v", *fixture.calls)
	}
	if fixture.aimili.username != "owner" || len(fixture.store.commits) != 0 {
		t.Fatal("failed external update changed committed account")
	}
}

func TestChangeRollsBackBothServicesWhenLocalCommitFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.store.commitErr = errors.New("database unavailable")
	err := fixture.coordinator.Change(context.Background(), ChangeRequest{Username: "renamed", Password: []byte("new-password-marker")})
	assertCoordinatorCode(t, err, "service_unavailable")
	joined := strings.Join(*fixture.calls, "|")
	if !strings.Contains(joined, "store.commit|xui.update:owner|aimili.update:owner") {
		t.Fatalf("missing local failure rollback: %#v", *fixture.calls)
	}
	if fixture.aimili.username != "owner" || fixture.xui.username != "owner" {
		t.Fatal("local commit failure left external accounts changed")
	}
}

func TestRollbackFailureMarksRepairRequiredWithoutLeakingPassword(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.store.commitErr = errors.New("database unavailable")
	fixture.xui.updateFailures["owner"] = errors.New("rollback unavailable")
	err := fixture.coordinator.Change(context.Background(), ChangeRequest{Username: "renamed", Password: []byte("new-password-marker")})
	assertCoordinatorCode(t, err, "repair_required")
	if fixture.store.state.Status != store.AccountSyncRepairRequired || fixture.store.state.ErrorCode != "rollback_failed" {
		t.Fatalf("repair state = %#v", fixture.store.state)
	}
	if strings.Contains(err.Error(), "new-password-marker") || strings.Contains(err.Error(), "old-password-marker") {
		t.Fatal("coordinator error exposed a password")
	}
}

func TestResetRequiredStateCanCompleteFirstAlignment(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.store.state.Status = store.AccountSyncResetRequired
	if err := fixture.coordinator.Repair(context.Background(), ChangeRequest{Username: "renamed", Password: []byte("new-password-marker")}); err != nil {
		t.Fatal(err)
	}
	if fixture.store.state.Status != store.AccountSyncSynced {
		t.Fatalf("first alignment state = %#v", fixture.store.state)
	}
}

func assertCoordinatorCode(t *testing.T, err error, want string) {
	t.Helper()
	var coordinatorError *Error
	if !errors.As(err, &coordinatorError) || coordinatorError.Code != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
