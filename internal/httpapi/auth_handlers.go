package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thzyh/aimili-gateway/internal/auth"
	"github.com/thzyh/aimili-gateway/internal/store"
)

const (
	sessionAbsoluteLifetime = 12 * time.Hour
	loginFailureWindow      = 15 * time.Minute
	loginFailureLimit       = 5
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTP     string `json:"totp"`
}

type reauthenticateRequest struct {
	Password string `json:"password"`
	TOTP     string `json:"totp"`
}

func (s *server) handleLogin(response http.ResponseWriter, request *http.Request) {
	if !s.requireOrigin(request) {
		writeAPIError(response, http.StatusForbidden, "forbidden")
		return
	}
	var input loginRequest
	if err := decodeJSON(request, &input); err != nil || input.Username == "" || input.Password == "" || input.TOTP == "" || len(input.Username) > 256 {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	now := s.now().UTC()
	limitKey := normalizedRemoteIP(request.RemoteAddr) + "|" + strings.ToLower(strings.TrimSpace(input.Username))
	if s.limiter.blocked(limitKey, now) {
		writeAPIError(response, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	valid, err := s.verifyCredentials(request, input.Username, input.Password, input.TOTP, true)
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	if !valid {
		s.limiter.recordFailure(limitKey, now)
		writeAPIError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.limiter.clear(limitKey)

	token := make([]byte, 32)
	csrf := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	if _, err := rand.Read(csrf); err != nil {
		clear(token)
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	defer clear(token)
	defer clear(csrf)
	tokenHash := sha256.Sum256(token)
	csrfHash := sha256.Sum256(csrf)
	expiresAt := now.Add(sessionAbsoluteLifetime)
	if err := s.store.CreateSession(request.Context(), store.Session{
		TokenHash:     tokenHash,
		CSRFTokenHash: csrfHash,
		CreatedAt:     now,
		LastActiveAt:  now,
		ExpiresAt:     expiresAt,
	}); err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	value := base64.RawURLEncoding.EncodeToString(token) + "." + base64.RawURLEncoding.EncodeToString(csrf)
	http.SetCookie(response, sessionCookie(value, expiresAt))
	response.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSession(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authenticateOrWrite(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"authenticated": true,
		"csrfToken":     session.csrfToken,
		"expiresAt":     session.stored.ExpiresAt,
	})
}

func (s *server) handleLogout(response http.ResponseWriter, request *http.Request) {
	if !s.requireOrigin(request) {
		writeAPIError(response, http.StatusForbidden, "forbidden")
		return
	}
	session, ok := s.authenticateOrWrite(response, request)
	if !ok {
		return
	}
	if !validCSRF(request, session) {
		writeAPIError(response, http.StatusForbidden, "forbidden")
		return
	}
	if err := s.store.RevokeSession(request.Context(), session.tokenHash); err != nil && !errors.Is(err, store.ErrSessionNotFound) {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	http.SetCookie(response, expiredSessionCookie())
	response.WriteHeader(http.StatusNoContent)
}

func (s *server) handleReauthenticate(response http.ResponseWriter, request *http.Request) {
	if !s.requireOrigin(request) {
		writeAPIError(response, http.StatusForbidden, "forbidden")
		return
	}
	session, ok := s.authenticateOrWrite(response, request)
	if !ok {
		return
	}
	if !validCSRF(request, session) {
		writeAPIError(response, http.StatusForbidden, "forbidden")
		return
	}
	var input reauthenticateRequest
	if err := decodeJSON(request, &input); err != nil || input.Password == "" || input.TOTP == "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	valid, err := s.verifyCredentials(request, "", input.Password, input.TOTP, false)
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	if !valid {
		writeAPIError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	now := s.now().UTC()
	if err := s.store.ReauthenticateSession(request.Context(), session.stored.ID, now); err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *server) verifyCredentials(request *http.Request, username, password, totp string, requireUsername bool) (bool, error) {
	admin, err := s.store.GetAdmin(request.Context())
	if errors.Is(err, store.ErrAdminNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	passwordBytes := []byte(password)
	defer clear(passwordBytes)
	passwordValid, err := auth.VerifyPassword(string(admin.PasswordHash), passwordBytes)
	if err != nil {
		return false, err
	}
	secret, err := auth.Open(s.masterKey, admin.TOTPSecretCiphertext)
	if err != nil {
		return false, err
	}
	defer clear(secret)
	totpValid := auth.ValidateTOTP(secret, totp, s.now().UTC())
	usernameValid := true
	if requireUsername {
		usernameValid = len(username) == len(admin.Username) && subtle.ConstantTimeCompare([]byte(username), []byte(admin.Username)) == 1
	}
	return usernameValid && passwordValid && totpValid, nil
}

func sessionCookie(value string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(sessionAbsoluteLifetime / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func expiredSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: make(map[string][]time.Time)}
}

func (l *loginLimiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key, now)
	return len(l.failures[key]) >= loginFailureLimit
}

func (l *loginLimiter) recordFailure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key, now)
	l.failures[key] = append(l.failures[key], now)
}

func (l *loginLimiter) clear(key string) {
	l.mu.Lock()
	delete(l.failures, key)
	l.mu.Unlock()
}

func (l *loginLimiter) prune(key string, now time.Time) {
	cutoff := now.Add(-loginFailureWindow)
	failures := l.failures[key]
	firstCurrent := 0
	for firstCurrent < len(failures) && !failures[firstCurrent].After(cutoff) {
		firstCurrent++
	}
	if firstCurrent == len(failures) {
		delete(l.failures, key)
		return
	}
	l.failures[key] = failures[firstCurrent:]
}

func normalizedRemoteIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}
