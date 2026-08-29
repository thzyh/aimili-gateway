package accountsync

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/auth"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type Error struct {
	Code string
}

func (err *Error) Error() string {
	return "account synchronization failed: " + err.Code
}

type ChangeRequest struct {
	Username string
	Password []byte
}

type accountStore interface {
	GetAdmin(context.Context) (store.Admin, error)
	LoadUnifiedCredentials(context.Context, []byte) (store.UnifiedCredentials, error)
	GetAccountSyncState(context.Context) (store.AccountSyncState, error)
	SetAccountSyncState(context.Context, store.AccountSyncState) error
	CommitUnifiedCredentialsAndRevokeSessions(context.Context, store.UnifiedCredentialUpdate) error
}

type aimiliAdmin interface {
	Capabilities(context.Context) (aimili.Capabilities, error)
	AdminStatus(context.Context) (aimili.AdminStatus, error)
	VerifyAdmin(context.Context, aimili.AdminUpdate) error
	UpdateAdmin(context.Context, aimili.AdminUpdate) error
}

type xuiAdmin interface {
	ProbeAdminCapabilities(context.Context) (xui.AdminCapabilities, error)
	VerifyAdmin(context.Context, xui.Credentials) error
	UpdateAdmin(context.Context, xui.Credentials, xui.Credentials) error
}

type Coordinator struct {
	store          accountStore
	masterKey      []byte
	aimili         aimiliAdmin
	xui            xuiAdmin
	now            func() time.Time
	hashPassword   func([]byte) (string, error)
	verifyPassword func(string, []byte) (bool, error)
	mu             sync.Mutex
}

func New(database accountStore, masterKey []byte, aimiliClient aimiliAdmin, xuiClient xuiAdmin, now func() time.Time) (*Coordinator, error) {
	if database == nil || len(masterKey) != 32 || aimiliClient == nil || xuiClient == nil {
		return nil, errors.New("account synchronization dependencies are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Coordinator{
		store:          database,
		masterKey:      append([]byte(nil), masterKey...),
		aimili:         aimiliClient,
		xui:            xuiClient,
		now:            now,
		hashPassword:   auth.HashPassword,
		verifyPassword: auth.VerifyPassword,
	}, nil
}

func (c *Coordinator) Status(ctx context.Context) (store.AccountSyncState, error) {
	return c.store.GetAccountSyncState(ctx)
}

func (c *Coordinator) Check(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	credentials, err := c.preflight(ctx)
	if err != nil {
		c.recordCheckFailure(ctx, err)
		return err
	}
	defer clear(credentials.Password)
	admin, err := c.store.GetAdmin(ctx)
	if err != nil || admin.Username != credentials.Username {
		drift := &Error{Code: "account_drift"}
		c.recordCheckFailure(ctx, drift)
		return drift
	}
	matches, err := c.verifyPassword(string(admin.PasswordHash), credentials.Password)
	if err != nil || !matches {
		drift := &Error{Code: "account_drift"}
		c.recordCheckFailure(ctx, drift)
		return drift
	}
	now := c.now().UTC()
	return c.store.SetAccountSyncState(ctx, store.AccountSyncState{
		Status:              store.AccountSyncSynced,
		UsernameFingerprint: usernameFingerprint(credentials.Username, c.masterKey),
		LastCheckedAt:       now,
	})
}

func (c *Coordinator) Change(ctx context.Context, request ChangeRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateChangeRequest(request); err != nil {
		return err
	}
	oldCredentials, err := c.preflight(ctx)
	if err != nil {
		c.recordCheckFailure(ctx, err)
		return err
	}
	defer clear(oldCredentials.Password)
	newPassword := append([]byte(nil), request.Password...)
	defer clear(newPassword)
	newCredentials := store.UnifiedCredentials{Username: request.Username, Password: newPassword}

	if err := c.aimili.UpdateAdmin(ctx, aimiliUpdate(newCredentials)); err != nil {
		return &Error{Code: "service_unavailable"}
	}
	aimiliChanged := true
	if err := c.xui.UpdateAdmin(ctx, xuiCredentials(oldCredentials), xuiCredentials(newCredentials)); err != nil {
		rollbackOK := c.restoreXUI(ctx, newCredentials, oldCredentials)
		if restoreErr := c.aimili.UpdateAdmin(ctx, aimiliUpdate(oldCredentials)); restoreErr != nil {
			rollbackOK = false
		}
		if !rollbackOK {
			return c.markRepairRequired(ctx)
		}
		return &Error{Code: "service_unavailable"}
	}
	xuiChanged := true

	if err := c.verifyLoginCredentials(ctx, newCredentials); err != nil {
		if !c.rollback(ctx, oldCredentials, newCredentials, xuiChanged, aimiliChanged) {
			return c.markRepairRequired(ctx)
		}
		return &Error{Code: "service_unavailable"}
	}

	passwordHash, err := c.hashPassword(newPassword)
	if err != nil {
		if !c.rollback(ctx, oldCredentials, newCredentials, xuiChanged, aimiliChanged) {
			return c.markRepairRequired(ctx)
		}
		return &Error{Code: "service_unavailable"}
	}
	usernameCiphertext, passwordCiphertext, err := store.SealUnifiedCredentials(request.Username, newPassword, c.masterKey)
	if err != nil {
		if !c.rollback(ctx, oldCredentials, newCredentials, xuiChanged, aimiliChanged) {
			return c.markRepairRequired(ctx)
		}
		return &Error{Code: "service_unavailable"}
	}
	admin, err := c.store.GetAdmin(ctx)
	if err != nil {
		if !c.rollback(ctx, oldCredentials, newCredentials, xuiChanged, aimiliChanged) {
			return c.markRepairRequired(ctx)
		}
		return &Error{Code: "service_unavailable"}
	}
	now := c.now().UTC()
	if err := c.store.CommitUnifiedCredentialsAndRevokeSessions(ctx, store.UnifiedCredentialUpdate{
		ExpectedSecurityUpdatedAt: admin.SecurityUpdatedAt,
		Username:                  request.Username,
		PasswordHash:              []byte(passwordHash),
		UsernameCiphertext:        usernameCiphertext,
		PasswordCiphertext:        passwordCiphertext,
		UsernameFingerprint:       usernameFingerprint(request.Username, c.masterKey),
		Status:                    store.AccountSyncSynced,
		UpdatedAt:                 now,
	}); err != nil {
		if !c.rollback(ctx, oldCredentials, newCredentials, xuiChanged, aimiliChanged) {
			return c.markRepairRequired(ctx)
		}
		return &Error{Code: "service_unavailable"}
	}
	if err := c.verifyCredentials(ctx, newCredentials); err != nil {
		return c.markRepairRequired(ctx)
	}
	return nil
}

func (c *Coordinator) Repair(ctx context.Context, request ChangeRequest) error {
	return c.Change(ctx, request)
}

func (c *Coordinator) preflight(ctx context.Context) (store.UnifiedCredentials, error) {
	capabilities, err := c.aimili.Capabilities(ctx)
	if err != nil || !containsAll(capabilities.Capabilities, "admin.read", "admin.verify", "admin.update", "admin.sessions.issue") {
		return store.UnifiedCredentials{}, &Error{Code: "version_incompatible"}
	}
	xuiCapabilities, err := c.xui.ProbeAdminCapabilities(ctx)
	if err != nil {
		return store.UnifiedCredentials{}, &Error{Code: "service_unavailable"}
	}
	if !xuiCapabilities.CanUpdate || !xuiCapabilities.CanBridge || !xuiCapabilities.TOTPCompatible {
		return store.UnifiedCredentials{}, &Error{Code: "version_incompatible"}
	}
	credentials, err := c.store.LoadUnifiedCredentials(ctx, c.masterKey)
	if err != nil {
		return store.UnifiedCredentials{}, &Error{Code: "account_drift"}
	}
	if err := c.verifyCredentials(ctx, credentials); err != nil {
		clear(credentials.Password)
		return store.UnifiedCredentials{}, &Error{Code: "account_drift"}
	}
	return credentials, nil
}

func (c *Coordinator) verifyCredentials(ctx context.Context, credentials store.UnifiedCredentials) error {
	status, err := c.aimili.AdminStatus(ctx)
	if err != nil || status.Username != credentials.Username {
		return &Error{Code: "account_drift"}
	}
	return c.verifyLoginCredentials(ctx, credentials)
}

func (c *Coordinator) verifyLoginCredentials(ctx context.Context, credentials store.UnifiedCredentials) error {
	if err := c.aimili.VerifyAdmin(ctx, aimiliUpdate(credentials)); err != nil {
		return &Error{Code: "account_drift"}
	}
	if err := c.xui.VerifyAdmin(ctx, xuiCredentials(credentials)); err != nil {
		return &Error{Code: "account_drift"}
	}
	return nil
}

func (c *Coordinator) rollback(ctx context.Context, oldCredentials, newCredentials store.UnifiedCredentials, xuiChanged, aimiliChanged bool) bool {
	rollbackOK := true
	if xuiChanged && !c.restoreXUI(ctx, newCredentials, oldCredentials) {
		rollbackOK = false
	}
	if aimiliChanged {
		if err := c.aimili.UpdateAdmin(ctx, aimiliUpdate(oldCredentials)); err != nil {
			rollbackOK = false
		}
	}
	return rollbackOK
}

func (c *Coordinator) restoreXUI(ctx context.Context, current, old store.UnifiedCredentials) bool {
	if err := c.xui.UpdateAdmin(ctx, xuiCredentials(current), xuiCredentials(old)); err == nil {
		return true
	}
	return c.xui.VerifyAdmin(ctx, xuiCredentials(old)) == nil
}

func (c *Coordinator) markRepairRequired(ctx context.Context) error {
	c.setFailureState(ctx, store.AccountSyncRepairRequired, "rollback_failed")
	return &Error{Code: "repair_required"}
}

func (c *Coordinator) recordCheckFailure(ctx context.Context, err error) {
	code := "service_unavailable"
	var syncError *Error
	if errors.As(err, &syncError) {
		code = syncError.Code
	}
	status := store.AccountSyncRepairRequired
	if code == "version_incompatible" {
		status = store.AccountSyncIncompatible
	}
	c.setFailureState(ctx, status, code)
}

func (c *Coordinator) setFailureState(ctx context.Context, status store.AccountSyncStatus, code string) {
	fingerprint := ""
	if current, err := c.store.GetAccountSyncState(ctx); err == nil {
		fingerprint = current.UsernameFingerprint
	}
	_ = c.store.SetAccountSyncState(ctx, store.AccountSyncState{
		Status:              status,
		UsernameFingerprint: fingerprint,
		LastCheckedAt:       c.now().UTC(),
		ErrorCode:           code,
	})
}

func validateChangeRequest(request ChangeRequest) error {
	if request.Username == "" || request.Username != strings.TrimSpace(request.Username) || len(request.Username) > 64 ||
		len(request.Password) < 12 || len(request.Password) > 256 || string(request.Password) != strings.TrimSpace(string(request.Password)) {
		return &Error{Code: "invalid_request"}
	}
	for _, character := range request.Username {
		if character < 0x20 || character == 0x7f {
			return &Error{Code: "invalid_request"}
		}
	}
	for _, character := range request.Password {
		if character < 0x20 || character == 0x7f {
			return &Error{Code: "invalid_request"}
		}
	}
	return nil
}

func aimiliUpdate(credentials store.UnifiedCredentials) aimili.AdminUpdate {
	return aimili.AdminUpdate{Username: credentials.Username, Password: credentials.Password}
}

func xuiCredentials(credentials store.UnifiedCredentials) xui.Credentials {
	return xui.Credentials{Username: credentials.Username, Password: string(credentials.Password)}
}

func containsAll(actual []string, wanted ...string) bool {
	present := make(map[string]bool, len(actual))
	for _, value := range actual {
		present[value] = true
	}
	for _, value := range wanted {
		if !present[value] {
			return false
		}
	}
	return true
}

func usernameFingerprint(username string, masterKey []byte) string {
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("aimili-gateway/account-username/v1\x00"))
	_, _ = mac.Write([]byte(username))
	return hex.EncodeToString(mac.Sum(nil))
}
