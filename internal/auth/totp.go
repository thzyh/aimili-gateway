package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"time"
)

const (
	totpSecretLength = 20
	totpStepSeconds  = 30
	totpModulo       = 1_000_000
)

func GenerateTOTPSecret() ([]byte, error) {
	secret := make([]byte, totpSecretLength)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func ValidateTOTP(secret []byte, code string, now time.Time) bool {
	if len(secret) == 0 || len(code) != 6 {
		return false
	}
	provided := int32(0)
	for index := range code {
		if code[index] < '0' || code[index] > '9' {
			return false
		}
		provided = provided*10 + int32(code[index]-'0')
	}
	step := now.Unix() / totpStepSeconds
	if step < 0 {
		return false
	}
	matched := 0
	for _, offset := range []int64{-1, 0, 1} {
		counter := step + offset
		if counter < 0 {
			continue
		}
		matched |= subtle.ConstantTimeEq(int32(totpCode(secret, uint64(counter))), provided)
	}
	return matched == 1
}

func totpCode(secret []byte, counter uint64) uint32 {
	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return value % totpModulo
}
