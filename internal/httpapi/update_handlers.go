package httpapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/store"
)

var updateRunIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var ErrUpdatesDisabled = errors.New("updates_disabled")

type UpdateVersion struct {
	Kind       string `json:"kind"`
	Version    string `json:"version"`
	Compatible bool   `json:"compatible"`
}

type UpdateSummary struct {
	Project        bool            `json:"project,omitempty"`
	ActiveRunID    string          `json:"activeRunId,omitempty"`
	Enabled        bool            `json:"enabled"`
	CurrentGateway string          `json:"currentGateway"`
	CurrentUI      string          `json:"currentUi,omitempty"`
	Available      []UpdateVersion `json:"available"`
}

type UpdateRequest struct {
	RunID   string
	Kind    string
	Version string
	Action  string
}

type UpdateResult struct {
	Phase     string `json:"phase,omitempty"`
	Percent   int    `json:"percent,omitempty"`
	Action    string `json:"action,omitempty"`
	RunID     string `json:"runId"`
	Kind      string `json:"kind"`
	Version   string `json:"version,omitempty"`
	State     string `json:"state"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type UpdateManager interface {
	List(context.Context) (UpdateSummary, error)
	Submit(context.Context, UpdateRequest) (UpdateResult, error)
	Get(context.Context, string) (UpdateResult, error)
}

type updateMutationInput struct {
	RunID string `json:"runId"`
}

func (s *server) handleListUpdates(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.updates == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "updates_disabled")
		return
	}
	summary, err := s.updates.List(request.Context())
	if err != nil {
		if errors.Is(err, ErrUpdatesDisabled) {
			writeAPIError(response, http.StatusServiceUnavailable, "updates_disabled")
			return
		}
		writeAPIError(response, http.StatusServiceUnavailable, "updates_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, summary)
}

func (s *server) handleApplyUpdate(response http.ResponseWriter, request *http.Request) {
	kind, version := request.PathValue("kind"), request.PathValue("version")
	if !validUpdateKind(kind) || !safeUpdateVersion(kind, version) {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	s.handleUpdateMutation(response, request, UpdateRequest{Kind: kind, Version: version, Action: "apply"}, kind == "ui")
}

func (s *server) handleRollbackUpdate(response http.ResponseWriter, request *http.Request) {
	kind := request.PathValue("kind")
	if !validUpdateKind(kind) {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	s.handleUpdateMutation(response, request, UpdateRequest{Kind: kind, Action: "rollback"}, false)
}

func (s *server) handleUpdateMutation(response http.ResponseWriter, request *http.Request, update UpdateRequest, requireAvailable bool) {
	accepted := false
	defer func() {
		// Only validated public identifiers enter audit metadata. No password,
		// URL, signature, local path or internal error text is retained.
		runFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(update.RunID)))
		action := "update." + update.Action + "." + update.Kind
		if update.Version != "" {
			action += "." + update.Version
		}
		result, code := store.AuditDenied, "request_rejected"
		if accepted {
			result, code = store.AuditSuccess, ""
		}
		_ = s.store.AppendAudit(request.Context(), store.AuditEvent{Action: action, ResourceType: "gateway_update", ResourceFingerprint: runFingerprint, Result: result, ErrorCode: code, CreatedAt: s.now()})
	}()
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
	if s.updates == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "updates_disabled")
		return
	}
	var input updateMutationInput
	if err := decodeJSON(request, &input); err != nil || !updateRunIDPattern.MatchString(input.RunID) {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	update.RunID = input.RunID
	summary, listErr := s.updates.List(request.Context())
	if listErr != nil || !summary.Enabled || (update.Kind == "project" && !summary.Project) {
		writeAPIError(response, http.StatusServiceUnavailable, "updates_disabled")
		return
	}
	if requireAvailable {
		found := false
		for _, available := range summary.Available {
			if available.Kind == update.Kind && available.Version == update.Version && available.Compatible {
				found = true
				break
			}
		}
		if !found {
			writeAPIError(response, http.StatusBadRequest, "unknown_update")
			return
		}
	}
	update.RunID = input.RunID
	result, err := s.updates.Submit(request.Context(), update)
	if err != nil {
		if errors.Is(err, ErrUpdatesDisabled) {
			writeAPIError(response, http.StatusServiceUnavailable, "updates_disabled")
			return
		}
		writeAPIError(response, http.StatusConflict, "update_busy")
		return
	}
	writeJSON(response, http.StatusAccepted, result)
	accepted = true
}

func (s *server) handleUpdateStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	runID := request.PathValue("runId")
	if s.updates == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "updates_disabled")
		return
	}
	if !updateRunIDPattern.MatchString(runID) {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := s.updates.Get(request.Context(), runID)
	if err != nil {
		if errors.Is(err, ErrUpdatesDisabled) {
			writeAPIError(response, http.StatusServiceUnavailable, "updates_disabled")
			return
		}
		writeAPIError(response, http.StatusNotFound, "update_not_found")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *server) handleCheckUpdates(response http.ResponseWriter, request *http.Request) {
	s.handleUpdateMutation(response, request, UpdateRequest{Kind: "project", Action: "check"}, false)
}

func validUpdateKind(kind string) bool { return kind == "ui" || kind == "gateway" || kind == "project" }

func safeUpdateVersion(kind, version string) bool {
	if kind == "project" {
		return strings.HasSuffix(version, "-vps") && safeUpdateVersion("gateway", strings.TrimSuffix(version, "-vps"))
	}
	if strings.ContainsAny(version, `/\\?#@% \t\r\n`) {
		return false
	}
	if kind == "ui" {
		return updateRunIDPattern.MatchString(version)
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if !strings.HasPrefix(version, "v") || len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}
