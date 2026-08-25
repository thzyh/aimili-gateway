package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/thzyh/aimili-gateway/internal/auth"
)

var ErrCredentialNotFound = errors.New("credential not found")

func (s *Store) PutCredential(ctx context.Context, purpose string, plaintext, masterKey []byte) error {
	if !validCredentialPurpose(purpose) || len(plaintext) == 0 {
		return errors.New("credential purpose and plaintext are required")
	}
	ciphertext, err := encryptCredential(purpose, plaintext, masterKey)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO encrypted_credentials(purpose, ciphertext, key_version, created_at, updated_at)
		VALUES(?, ?, 1, ?, ?)
		ON CONFLICT(purpose) DO UPDATE SET ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		purpose, ciphertext, now, now)
	if err != nil {
		return fmt.Errorf("store encrypted credential: %w", err)
	}
	return nil
}

func (s *Store) GetCredential(ctx context.Context, purpose string, masterKey []byte) ([]byte, error) {
	if !validCredentialPurpose(purpose) {
		return nil, errors.New("invalid credential purpose")
	}
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT ciphertext FROM encrypted_credentials WHERE purpose = ?`, purpose).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get encrypted credential: %w", err)
	}
	return decryptCredential(purpose, ciphertext, masterKey)
}

func encryptCredential(purpose string, plaintext, masterKey []byte) ([]byte, error) {
	key, err := deriveCredentialKey(purpose, masterKey)
	if err != nil {
		return nil, err
	}
	return auth.Seal(key, plaintext)
}

func decryptCredential(purpose string, ciphertext, masterKey []byte) ([]byte, error) {
	key, err := deriveCredentialKey(purpose, masterKey)
	if err != nil {
		return nil, err
	}
	return auth.Open(key, ciphertext)
}

func deriveCredentialKey(purpose string, masterKey []byte) ([]byte, error) {
	if len(masterKey) != 32 || !validCredentialPurpose(purpose) {
		return nil, errors.New("invalid credential encryption context")
	}
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("aimili-gateway/credential/v1\x00" + purpose))
	return mac.Sum(nil), nil
}

func validCredentialPurpose(purpose string) bool {
	if len(purpose) < 1 || len(purpose) > 64 {
		return false
	}
	for _, character := range purpose {
		if !(character == '-' || character >= 'a' && character <= 'z') {
			return false
		}
	}
	return true
}

func (s *Store) ReplaceMixedCIDRs(ctx context.Context, prefixes []netip.Prefix) error {
	canonical := make([]string, 0, len(prefixes))
	seen := make(map[string]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		if !prefix.IsValid() || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return errors.New("mixed source CIDR must be a canonical non-global prefix")
		}
		text := prefix.String()
		if _, exists := seen[text]; exists {
			continue
		}
		seen[text] = struct{}{}
		canonical = append(canonical, text)
	}
	sort.Strings(canonical)
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mixed CIDR update: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `DELETE FROM mixed_source_cidrs`); err != nil {
		return fmt.Errorf("clear mixed CIDRs: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	for _, prefix := range canonical {
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO mixed_source_cidrs(prefix, created_at) VALUES(?, ?)`, prefix, now); err != nil {
			return fmt.Errorf("insert mixed CIDR: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit mixed CIDR update: %w", err)
	}
	return nil
}

func (s *Store) ListMixedCIDRs(ctx context.Context) ([]netip.Prefix, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT prefix FROM mixed_source_cidrs ORDER BY prefix`)
	if err != nil {
		return nil, fmt.Errorf("list mixed CIDRs: %w", err)
	}
	defer rows.Close()
	result := make([]netip.Prefix, 0)
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, fmt.Errorf("scan mixed CIDR: %w", err)
		}
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return nil, errors.New("stored mixed CIDR is invalid")
		}
		result = append(result, prefix)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list mixed CIDRs: %w", err)
	}
	return result, nil
}
