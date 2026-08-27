package backendlogin

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/accountsync"
	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type Target string

const (
	TargetAimiliVPN Target = "aimilivpn"
	TargetXUI       Target = "3x-ui"
)

type Error struct{ Code string }

func (err *Error) Error() string { return "backend login failed: " + err.Code }

type Config struct {
	AimiliPath     string
	AimiliLocation string
	XUIPath        string
	XUILocation    string
}

type Session struct {
	CookieName string
	Token      []byte
	Path       string
	Location   string
	ExpiresAt  time.Time
}

type accountChecker interface {
	Check(context.Context) error
	Status(context.Context) (store.AccountSyncState, error)
}

type credentialStore interface {
	LoadUnifiedCredentials(context.Context, []byte) (store.UnifiedCredentials, error)
}

type aimiliSessionIssuer interface {
	IssueAdminSession(context.Context) (aimili.AdminSession, error)
}

type xuiSessionIssuer interface {
	IssueAdminSession(context.Context, xui.Credentials) (xui.BrowserSession, error)
}

type Service struct {
	config      Config
	accounts    accountChecker
	credentials credentialStore
	aimili      aimiliSessionIssuer
	xui         xuiSessionIssuer
	masterKey   []byte
}

func New(config Config, accounts accountChecker, credentials credentialStore, aimiliIssuer aimiliSessionIssuer, xuiIssuer xuiSessionIssuer, masterKey []byte) (*Service, error) {
	for _, value := range []string{config.AimiliPath, config.AimiliLocation, config.XUIPath, config.XUILocation} {
		if !validFixedPath(value) {
			return nil, errors.New("backend login requires fixed same-origin paths")
		}
	}
	if accounts == nil || credentials == nil || aimiliIssuer == nil || xuiIssuer == nil || len(masterKey) != 32 {
		return nil, errors.New("backend login dependencies are required")
	}
	return &Service{config: config, accounts: accounts, credentials: credentials, aimili: aimiliIssuer, xui: xuiIssuer, masterKey: append([]byte(nil), masterKey...)}, nil
}

func (service *Service) Login(ctx context.Context, target Target) (Session, error) {
	if target != TargetAimiliVPN && target != TargetXUI {
		return Session{}, &Error{Code: "invalid_target"}
	}
	if err := service.accounts.Check(ctx); err != nil {
		return Session{}, mapAccountError(err)
	}
	state, err := service.accounts.Status(ctx)
	if err != nil || state.Status != store.AccountSyncSynced {
		if state.Status == store.AccountSyncIncompatible {
			return Session{}, &Error{Code: "version_incompatible"}
		}
		return Session{}, &Error{Code: "account_drift"}
	}
	if target == TargetAimiliVPN {
		issued, err := service.aimili.IssueAdminSession(ctx)
		if err != nil || issued.CookieName != "session" || !validToken(issued.Token) {
			return Session{}, &Error{Code: "automatic_login_failed"}
		}
		return Session{CookieName: issued.CookieName, Token: append([]byte(nil), issued.Token...), Path: service.config.AimiliPath, Location: service.config.AimiliLocation, ExpiresAt: issued.ExpiresAt}, nil
	}
	credentials, err := service.credentials.LoadUnifiedCredentials(ctx, service.masterKey)
	if err != nil {
		return Session{}, &Error{Code: "account_drift"}
	}
	defer clear(credentials.Password)
	issued, err := service.xui.IssueAdminSession(ctx, xui.Credentials{Username: credentials.Username, Password: string(credentials.Password)})
	if err != nil || issued.CookieName != "session" || !validToken(issued.Token) {
		return Session{}, &Error{Code: "automatic_login_failed"}
	}
	return Session{CookieName: issued.CookieName, Token: append([]byte(nil), issued.Token...), Path: service.config.XUIPath, Location: service.config.XUILocation}, nil
}

func mapAccountError(err error) error {
	var syncError *accountsync.Error
	if errors.As(err, &syncError) {
		switch syncError.Code {
		case "version_incompatible":
			return &Error{Code: "version_incompatible"}
		case "service_unavailable":
			return &Error{Code: "service_unavailable"}
		}
	}
	return &Error{Code: "account_drift"}
}

func validFixedPath(value string) bool {
	if value == "" || !strings.HasPrefix(value, "/") || !strings.HasSuffix(value, "/") || strings.HasPrefix(value, "//") || path.Clean(value)+"/" != value {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == value
}

func validToken(token []byte) bool {
	if len(token) < 16 || len(token) > 4096 {
		return false
	}
	for _, character := range token {
		if character <= 0x20 || character == 0x7f || character == ';' || character == ',' {
			return false
		}
	}
	return true
}
