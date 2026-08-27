package accountsync

import (
	"context"

	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/store"
)

// MigrateLegacyCredentials refreshes the one-time migration input until a
// unified account change has been committed. A non-empty fingerprint is the
// durable boundary after which the legacy file can no longer overwrite the
// encrypted unified credentials.
func MigrateLegacyCredentials(ctx context.Context, database *store.Store, legacyPath string, masterKey []byte) error {
	state, err := database.GetAccountSyncState(ctx)
	if err != nil {
		return err
	}
	if state.UsernameFingerprint != "" {
		return nil
	}
	legacy, err := xui.ReadCredentialsFile(legacyPath)
	if err != nil {
		return err
	}
	password := []byte(legacy.Password)
	defer clear(password)
	if err := database.PutCredential(ctx, store.UnifiedUsernamePurpose, []byte(legacy.Username), masterKey); err != nil {
		return err
	}
	return database.PutCredential(ctx, store.UnifiedPasswordPurpose, password, masterKey)
}
