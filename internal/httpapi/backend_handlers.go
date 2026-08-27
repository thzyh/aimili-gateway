package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/thzyh/aimili-gateway/internal/backendlogin"
)

func (s *server) handleAimiliBackendLogin(response http.ResponseWriter, request *http.Request) {
	s.handleBackendLogin(response, request, backendlogin.TargetAimiliVPN)
}

func (s *server) handleXUIBackendLogin(response http.ResponseWriter, request *http.Request) {
	s.handleBackendLogin(response, request, backendlogin.TargetXUI)
}

func (s *server) handleBackendLogin(response http.ResponseWriter, request *http.Request, target backendlogin.Target) {
	if _, ok := s.authorizeSessionMutation(response, request); !ok {
		return
	}
	if s.backendLogin == nil {
		writeBackendLoginError(response, http.StatusServiceUnavailable, "service_unavailable")
		return
	}
	if !emptyRequestBody(request) {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	session, err := s.backendLogin.Login(request.Context(), target)
	if err != nil {
		code := "automatic_login_failed"
		var loginError *backendlogin.Error
		if errors.As(err, &loginError) {
			code = loginError.Code
		}
		status := http.StatusServiceUnavailable
		if code == "account_drift" || code == "version_incompatible" {
			status = http.StatusConflict
		}
		writeBackendLoginError(response, status, code)
		return
	}
	defer clear(session.Token)
	cookie := &http.Cookie{
		Name: session.CookieName, Value: string(session.Token), Path: session.Path,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	}
	if !session.ExpiresAt.IsZero() {
		cookie.Expires = session.ExpiresAt.UTC()
		if remaining := time.Until(cookie.Expires); remaining > 0 {
			cookie.MaxAge = int(remaining / time.Second)
		}
	}
	http.SetCookie(response, cookie)
	response.Header().Set("Location", session.Location)
	response.WriteHeader(http.StatusSeeOther)
}

func writeBackendLoginError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]any{"error": code, "manualLogin": true})
}

func emptyRequestBody(request *http.Request) bool {
	if request.Body == nil {
		return true
	}
	var buffer [1]byte
	count, err := request.Body.Read(buffer[:])
	return count == 0 && errors.Is(err, io.EOF)
}
