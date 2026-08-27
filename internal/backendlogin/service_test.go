package backendlogin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestServiceIssuesOnlyFixedBackendSessionsAfterAccountCheck(t *testing.T) {
	accounts := &fakeAccounts{state: store.AccountSyncState{Status: store.AccountSyncSynced}}
	credentials := &fakeCredentialStore{value: store.UnifiedCredentials{Username: "owner", Password: []byte("not-returned-password")}}
	aimiliIssuer := &fakeAimiliIssuer{session: aimili.AdminSession{CookieName: "session", Token: []byte("opaque-aimili-session-token-value"), ExpiresAt: time.Unix(1_700_000_300, 0).UTC()}}
	xuiIssuer := &fakeXUIIssuer{session: xui.BrowserSession{CookieName: "3x-ui", Token: []byte("opaque-xui-session-token")}}
	service, err := New(Config{
		AimiliPath: "/aimili-native-fixture/", AimiliLocation: "/aimili-native-fixture/",
		XUIPath: "/xui-native-fixture/", XUILocation: "/xui-native-fixture/",
	}, accounts, credentials, aimiliIssuer, xuiIssuer, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}

	aimiliSession, err := service.Login(context.Background(), TargetAimiliVPN)
	if err != nil {
		t.Fatal(err)
	}
	if aimiliSession.CookieName != "session" || aimiliSession.Path != "/aimili-native-fixture/" || aimiliSession.Location != "/aimili-native-fixture/" || len(aimiliSession.Token) == 0 {
		t.Fatalf("Aimili session = %#v", aimiliSession)
	}
	xuiSession, err := service.Login(context.Background(), TargetXUI)
	if err != nil {
		t.Fatal(err)
	}
	if xuiSession.CookieName != "3x-ui" || xuiSession.Path != "/xui-native-fixture/" || xuiSession.Location != "/xui-native-fixture/" || len(xuiSession.Token) == 0 {
		t.Fatalf("3x-ui session = %#v", xuiSession)
	}
	if accounts.checkCalls != 2 || aimiliIssuer.calls != 1 || xuiIssuer.calls != 1 || xuiIssuer.username != "owner" || xuiIssuer.password != "not-returned-password" {
		t.Fatalf("calls check=%d aimili=%d xui=%d user=%q", accounts.checkCalls, aimiliIssuer.calls, xuiIssuer.calls, xuiIssuer.username)
	}
}

func TestServiceFailsClosedWhenAccountsAreNotSynchronized(t *testing.T) {
	accounts := &fakeAccounts{checkErr: errors.New("drift"), state: store.AccountSyncState{Status: store.AccountSyncRepairRequired}}
	service, err := New(Config{AimiliPath: "/aimili/", AimiliLocation: "/aimili/", XUIPath: "/xui/", XUILocation: "/xui/"}, accounts, &fakeCredentialStore{}, &fakeAimiliIssuer{}, &fakeXUIIssuer{}, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(context.Background(), TargetAimiliVPN); codeOf(err) != "account_drift" {
		t.Fatalf("error = %v", err)
	}
}

func TestNewRejectsNonExactBackendPaths(t *testing.T) {
	for _, config := range []Config{
		{AimiliPath: "https://evil.invalid/", AimiliLocation: "/aimili/", XUIPath: "/xui/", XUILocation: "/xui/"},
		{AimiliPath: "/aimili/", AimiliLocation: "//evil.invalid/", XUIPath: "/xui/", XUILocation: "/xui/"},
		{AimiliPath: "/aimili/", AimiliLocation: "/aimili/", XUIPath: "/xui/?target=bad", XUILocation: "/xui/"},
	} {
		if _, err := New(config, &fakeAccounts{}, &fakeCredentialStore{}, &fakeAimiliIssuer{}, &fakeXUIIssuer{}, []byte("01234567890123456789012345678901")); err == nil {
			t.Fatalf("unsafe config accepted: %#v", config)
		}
	}
}

type fakeAccounts struct {
	state      store.AccountSyncState
	checkErr   error
	checkCalls int
}

func (fake *fakeAccounts) Check(context.Context) error { fake.checkCalls++; return fake.checkErr }
func (fake *fakeAccounts) Status(context.Context) (store.AccountSyncState, error) {
	return fake.state, nil
}

type fakeCredentialStore struct{ value store.UnifiedCredentials }

func (fake *fakeCredentialStore) LoadUnifiedCredentials(context.Context, []byte) (store.UnifiedCredentials, error) {
	return store.UnifiedCredentials{Username: fake.value.Username, Password: append([]byte(nil), fake.value.Password...)}, nil
}

type fakeAimiliIssuer struct {
	session aimili.AdminSession
	calls   int
}

func (fake *fakeAimiliIssuer) IssueAdminSession(context.Context) (aimili.AdminSession, error) {
	fake.calls++
	return fake.session, nil
}

type fakeXUIIssuer struct {
	session  xui.BrowserSession
	calls    int
	username string
	password string
}

func (fake *fakeXUIIssuer) IssueAdminSession(_ context.Context, credentials xui.Credentials) (xui.BrowserSession, error) {
	fake.calls++
	fake.username = credentials.Username
	fake.password = credentials.Password
	return fake.session, nil
}

func codeOf(err error) string {
	var serviceError *Error
	if errors.As(err, &serviceError) {
		return serviceError.Code
	}
	return ""
}
