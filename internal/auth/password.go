package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMemory      uint32 = 64 * 1024
	passwordIterations  uint32 = 3
	passwordParallelism uint8  = 2
	passwordSaltLength         = 16
	passwordHashLength  uint32 = 32
)

func HashPassword(password []byte) (string, error) {
	if len(password) == 0 {
		return "", errors.New("password is required")
	}
	salt := make([]byte, passwordSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey(password, salt, passwordIterations, passwordMemory, passwordParallelism, passwordHashLength)
	defer clear(hash)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		passwordMemory,
		passwordIterations,
		passwordParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded string, password []byte) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("invalid password hash format")
	}
	version, err := parseParameter(parts[2], "v=")
	if err != nil || version != uint64(argon2.Version) {
		return false, errors.New("invalid password hash version")
	}
	parameters := strings.Split(parts[3], ",")
	if len(parameters) != 3 {
		return false, errors.New("invalid password hash parameters")
	}
	memory, memoryErr := parseParameter(parameters[0], "m=")
	iterations, iterationsErr := parseParameter(parameters[1], "t=")
	parallelism, parallelismErr := parseParameter(parameters[2], "p=")
	if memoryErr != nil || iterationsErr != nil || parallelismErr != nil ||
		memory != uint64(passwordMemory) || iterations != uint64(passwordIterations) || parallelism != uint64(passwordParallelism) {
		return false, errors.New("invalid password hash parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != passwordSaltLength {
		return false, errors.New("invalid password hash salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != int(passwordHashLength) {
		return false, errors.New("invalid password hash digest")
	}
	actual := argon2.IDKey(password, salt, passwordIterations, passwordMemory, passwordParallelism, passwordHashLength)
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parseParameter(value, prefix string) (uint64, error) {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return 0, errors.New("missing parameter")
	}
	return strconv.ParseUint(value[len(prefix):], 10, 32)
}
