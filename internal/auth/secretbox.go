package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const secretboxFormatVersion byte = 1

func Seal(masterKey, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(masterKey)
	if err != nil {
		return nil, err
	}
	sealed := make([]byte, 1+gcm.NonceSize())
	sealed[0] = secretboxFormatVersion
	nonce := sealed[1:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return gcm.Seal(sealed, nonce, plaintext, sealed[:1]), nil
}

func Open(masterKey, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(masterKey)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || ciphertext[0] != secretboxFormatVersion {
		return nil, errors.New("invalid encrypted secret format")
	}
	minimumLength := 1 + gcm.NonceSize() + gcm.Overhead()
	if len(ciphertext) < minimumLength {
		return nil, errors.New("encrypted secret is truncated")
	}
	nonce := ciphertext[1 : 1+gcm.NonceSize()]
	plaintext, err := gcm.Open(nil, nonce, ciphertext[1+gcm.NonceSize():], ciphertext[:1])
	if err != nil {
		return nil, errors.New("encrypted secret authentication failed")
	}
	return plaintext, nil
}

func newGCM(masterKey []byte) (cipher.AEAD, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("master key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, errors.New("initialize encrypted secret")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize encrypted secret")
	}
	return gcm, nil
}
