package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/store"
)

const sessionIdleTimeout = 30 * time.Minute

var errUnauthenticated = errors.New("unauthenticated")

type requestSession struct {
	stored    store.Session
	tokenHash [32]byte
	csrfToken string
}

func (s *server) authenticateRequest(request *http.Request) (requestSession, error) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return requestSession{}, errUnauthenticated
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return requestSession{}, errUnauthenticated
	}
	token, tokenErr := base64.RawURLEncoding.DecodeString(parts[0])
	csrf, csrfErr := base64.RawURLEncoding.DecodeString(parts[1])
	if tokenErr != nil || csrfErr != nil || len(token) != 32 || len(csrf) != 32 {
		return requestSession{}, errUnauthenticated
	}
	tokenHash := sha256.Sum256(token)
	csrfHash := sha256.Sum256(csrf)
	now := s.now().UTC()
	stored, err := s.store.GetSession(request.Context(), tokenHash, now)
	if errors.Is(err, store.ErrSessionNotFound) {
		return requestSession{}, errUnauthenticated
	}
	if err != nil {
		return requestSession{}, err
	}
	if subtle.ConstantTimeCompare(stored.CSRFTokenHash[:], csrfHash[:]) != 1 {
		return requestSession{}, errUnauthenticated
	}
	if !now.Before(stored.LastActiveAt.Add(sessionIdleTimeout)) {
		_ = s.store.RevokeSession(request.Context(), tokenHash)
		return requestSession{}, errUnauthenticated
	}
	if err := s.store.TouchSession(request.Context(), stored.ID, now); err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return requestSession{}, errUnauthenticated
		}
		return requestSession{}, err
	}
	stored.LastActiveAt = now
	return requestSession{stored: stored, tokenHash: tokenHash, csrfToken: parts[1]}, nil
}

func (s *server) requireOrigin(request *http.Request) bool {
	return request.Header.Get("Origin") == s.allowedOrigin
}

func validCSRF(request *http.Request, session requestSession) bool {
	provided := request.Header.Get("X-CSRF-Token")
	return len(provided) == len(session.csrfToken) && subtle.ConstantTimeCompare([]byte(provided), []byte(session.csrfToken)) == 1
}

func (s *server) authenticateOrWrite(response http.ResponseWriter, request *http.Request) (requestSession, bool) {
	session, err := s.authenticateRequest(request)
	if errors.Is(err, errUnauthenticated) {
		writeAPIError(response, http.StatusUnauthorized, "unauthorized")
		return requestSession{}, false
	}
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return requestSession{}, false
	}
	return session, true
}
