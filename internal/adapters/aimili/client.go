package aimili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/securefile"
)

const (
	controlReadTimeout      = 8 * time.Second
	controlOperationTimeout = 75 * time.Second
	mainAssignmentTimeout   = 195 * time.Second
	controlResponseLimit    = 16 << 10
)

type Capabilities struct {
	APIVersion     string   `json:"apiVersion"`
	ServiceVersion string   `json:"serviceVersion"`
	Capabilities   []string `json:"capabilities"`
}

type Candidate struct {
	ID          string  `json:"id"`
	CountryCode string  `json:"country_short"`
	CountryName string  `json:"country"`
	IP          string  `json:"ip"`
	ProxyType   string  `json:"proxy_type"`
	Owner       string  `json:"owner"`
	ASN         string  `json:"asn"`
	ASName      string  `json:"as_name"`
	LatencyMS   int     `json:"latency_ms"`
	Score       int     `json:"score"`
	ProbeStatus string  `json:"probe_status"`
	LastProbeAt float64 `json:"last_probe_at"`
}

type CandidateCountry struct {
	Code                   string  `json:"code"`
	Name                   string  `json:"name"`
	CandidateCount         int     `json:"candidateCount"`
	ObservedAt             float64 `json:"observedAt"`
	OfficialCandidateTotal int     `json:"officialCandidateTotal"`
	ValidNodeCount         int     `json:"validNodeCount"`
	ValidCountryCount      int     `json:"validCountryCount"`
}

type CountryRefresh struct {
	State                 string  `json:"state"`
	Country               string  `json:"country"`
	Phase                 string  `json:"phase"`
	CatalogCount          int     `json:"catalogCount"`
	CountryCandidateCount int     `json:"countryCandidateCount"`
	TestedCount           int     `json:"testedCount"`
	ValidCount            int     `json:"validCount"`
	PreservedCount        int     `json:"preservedCount"`
	StartedAt             float64 `json:"startedAt"`
	FinishedAt            float64 `json:"finishedAt"`
	ErrorCode             string  `json:"errorCode"`
	StopReason            string  `json:"stopReason"`
	CacheTotal            int     `json:"cacheTotal"`
	CountryValidCount     int     `json:"countryValidCount"`
}

type CreateSlotRequest struct {
	Country     string `json:"country"`
	ProxyType   string `json:"proxyType"`
	CandidateID string `json:"candidateId,omitempty"`
}

type AssignSlotRequest struct {
	CandidateID string `json:"candidateId"`
	Country     string `json:"country"`
	ProxyType   string `json:"proxyType"`
}

type Slot struct {
	Number      int     `json:"slot"`
	Country     string  `json:"country"`
	CountryName string  `json:"country_name"`
	ProxyType   string  `json:"proxy_type"`
	Port        int     `json:"port"`
	Status      string  `json:"status"`
	NodeID      string  `json:"node_id"`
	CandidateIP string  `json:"candidate_ip"`
	ExitIP      string  `json:"exit_ip"`
	EgressOK    bool    `json:"egress_ok"`
	OK          bool    `json:"ok"`
	LatencyMS   int     `json:"latency_ms"`
	CheckedAt   float64 `json:"checked_at"`
}

type SlotCheck = Slot

type MainStatus struct {
	CandidateID string `json:"candidate_id"`
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	ProxyType   string `json:"proxy_type"`
	ExitIP      string `json:"exit_ip"`
	Port        int    `json:"port"`
	EgressOK    bool   `json:"egress_ok"`
	Active      bool   `json:"active"`
}

type MutationLease struct {
	State     string  `json:"state"`
	LeaseID   string  `json:"lease_id"`
	ExpiresAt float64 `json:"expires_at"`
}

type MainAssignmentRequest struct {
	CandidateID                string `json:"candidateId"`
	Country                    string `json:"country"`
	ProxyType                  string `json:"proxyType"`
	ExpectedCurrentCandidateID string `json:"expectedCurrentCandidateId"`
	IdempotencyKey             string `json:"idempotencyKey"`
}

type MainRepairRequest struct {
	CandidateID string `json:"candidateId"`
	Country     string `json:"country"`
	ProxyType   string `json:"proxyType"`
}

type MainAssignmentStatus struct {
	OperationID    string  `json:"operation_id"`
	State          string  `json:"state"`
	OldCandidateID string  `json:"old_candidate_id"`
	NewCandidateID string  `json:"new_candidate_id"`
	Country        string  `json:"country"`
	ProxyType      string  `json:"proxy_type"`
	Port           int     `json:"port"`
	DNSVerified    bool    `json:"dns_verified"`
	ExitVerified   bool    `json:"exit_verified"`
	Available      bool    `json:"available"`
	ErrorCode      string  `json:"error_code"`
	Resolution     string  `json:"resolution"`
	ExpiresAt      float64 `json:"expires_at"`
}

type AdminStatus struct {
	Username      string `json:"username"`
	TOTPSupported bool   `json:"totpSupported"`
}

type AdminUpdate struct {
	Username string
	Password []byte
}

type AdminSession struct {
	CookieName string
	Token      []byte
	ExpiresAt  time.Time
}

type AdapterError struct {
	Code string
}

func (e *AdapterError) Error() string {
	return "aimili control request failed: " + e.Code
}

type Client struct {
	baseURL          *url.URL
	token            string
	httpClient       *http.Client
	readTimeout      time.Duration
	operationTimeout time.Duration
}

func ReadTokenFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("open Aimili control token file")
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 4096 {
		return nil, errors.New("invalid Aimili control token file")
	}
	if !securefile.RestrictedPermissions(path, info.Mode()) {
		return nil, errors.New("Aimili control token file permissions are too broad")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read Aimili control token file")
	}
	token := []byte(strings.TrimSpace(string(raw)))
	if len(token) == 0 {
		return nil, errors.New("Aimili control token is empty")
	}
	return token, nil
}

func NewClient(baseURL string, token []byte) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid Aimili control URL")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("Aimili control URL must use loopback")
	}
	secret := strings.TrimSpace(string(token))
	if secret == "" {
		return nil, errors.New("Aimili control token is required")
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return &Client{
		baseURL:          parsed,
		token:            secret,
		readTimeout:      controlReadTimeout,
		operationTimeout: controlOperationTimeout,
		httpClient: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var result Capabilities
	err := c.do(ctx, c.readTimeout, http.MethodGet, "control/v1/capabilities", nil, &result)
	return result, err
}

func (c *Client) Candidates(ctx context.Context) ([]Candidate, error) {
	var result []Candidate
	err := c.do(ctx, c.readTimeout, http.MethodGet, "control/v1/candidates", nil, &result)
	return result, err
}

func (c *Client) CandidateCountries(ctx context.Context) ([]CandidateCountry, error) {
	var result []CandidateCountry
	if err := c.doAllowUnknown(ctx, c.readTimeout, http.MethodGet, "control/v1/candidates/countries", nil, &result); err != nil {
		return nil, err
	}
	for _, country := range result {
		if len(country.Code) != 2 || country.Code != strings.ToUpper(country.Code) || country.CandidateCount < 0 || country.ObservedAt < 0 || country.OfficialCandidateTotal < 0 || country.ValidNodeCount < 0 || country.ValidCountryCount < 0 {
			return nil, &AdapterError{Code: "invalid_response"}
		}
	}
	return result, nil
}

func (c *Client) StartCountryRefresh(ctx context.Context, country string) (CountryRefresh, error) {
	normalized := strings.ToUpper(strings.TrimSpace(country))
	if len(normalized) != 2 || normalized[0] < 'A' || normalized[0] > 'Z' || normalized[1] < 'A' || normalized[1] > 'Z' {
		return CountryRefresh{}, &AdapterError{Code: "invalid_request"}
	}
	var result CountryRefresh
	input := struct {
		Country string `json:"country"`
	}{Country: normalized}
	if err := c.doAllowUnknown(ctx, c.readTimeout, http.MethodPost, "control/v1/candidates/refresh", input, &result); err != nil {
		return CountryRefresh{}, err
	}
	if !validCountryRefresh(result) {
		return CountryRefresh{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) CountryRefresh(ctx context.Context) (CountryRefresh, error) {
	var result CountryRefresh
	if err := c.doAllowUnknown(ctx, c.readTimeout, http.MethodGet, "control/v1/candidates/refresh", nil, &result); err != nil {
		return CountryRefresh{}, err
	}
	if !validCountryRefresh(result) {
		return CountryRefresh{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func validCountryRefresh(refresh CountryRefresh) bool {
	switch refresh.State {
	case "idle", "running", "completed", "failed":
	default:
		return false
	}
	if refresh.Country != "" && (len(refresh.Country) != 2 || refresh.Country != strings.ToUpper(refresh.Country)) {
		return false
	}
	return refresh.CatalogCount >= 0 && refresh.CountryCandidateCount >= 0 && refresh.TestedCount >= 0 &&
		refresh.ValidCount >= 0 && refresh.PreservedCount >= 0 && refresh.StartedAt >= 0 && refresh.FinishedAt >= 0 &&
		refresh.CacheTotal >= 0 && refresh.CountryValidCount >= 0
}

func (c *Client) CreateSlot(ctx context.Context, input CreateSlotRequest) (Slot, error) {
	var result Slot
	err := c.do(ctx, c.operationTimeout, http.MethodPost, "control/v1/slots", input, &result)
	return result, err
}

func (c *Client) ListSlots(ctx context.Context) ([]Slot, error) {
	var result []Slot
	err := c.do(ctx, c.readTimeout, http.MethodGet, "control/v1/slots", nil, &result)
	return result, err
}

func (c *Client) MainStatus(ctx context.Context) (MainStatus, error) {
	var result MainStatus
	if err := c.doAllowUnknown(ctx, c.readTimeout, http.MethodGet, "control/v1/main", nil, &result); err != nil {
		return MainStatus{}, err
	}
	if result.Country != "" && (len(result.Country) != 2 || result.Country != strings.ToUpper(result.Country)) {
		return MainStatus{}, &AdapterError{Code: "invalid_response"}
	}
	if result.ProxyType != "" && !domainProxyTypeValid(result.ProxyType) {
		return MainStatus{}, &AdapterError{Code: "invalid_response"}
	}
	if result.Port < 0 || result.Port > 65535 || (result.ExitIP != "" && net.ParseIP(result.ExitIP) == nil) {
		return MainStatus{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

var safeMainOperationID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func (c *Client) AcquireMutationLease(ctx context.Context, idempotencyKey string) (MutationLease, error) {
	if !visibleNonWhitespaceASCII(idempotencyKey, 8, 256) {
		return MutationLease{}, &AdapterError{Code: "invalid_request"}
	}
	input := struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}{IdempotencyKey: idempotencyKey}
	var result MutationLease
	if err := c.do(ctx, c.operationTimeout, http.MethodPost, "control/v1/mutation-leases", input, &result); err != nil {
		return MutationLease{}, err
	}
	if !validMutationLease(result) {
		return MutationLease{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) RenewMutationLease(ctx context.Context, leaseID string) (MutationLease, error) {
	if !safeMutationLeaseID(leaseID) {
		return MutationLease{}, &AdapterError{Code: "invalid_request"}
	}
	var result MutationLease
	path := fmt.Sprintf("control/v1/mutation-leases/%s/renew", leaseID)
	if err := c.do(ctx, c.operationTimeout, http.MethodPost, path, struct{}{}, &result); err != nil {
		return MutationLease{}, err
	}
	if !validMutationLease(result) || result.LeaseID != leaseID {
		return MutationLease{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) ReleaseMutationLease(ctx context.Context, leaseID string) error {
	if !safeMutationLeaseID(leaseID) {
		return &AdapterError{Code: "invalid_request"}
	}
	var result struct {
		State string `json:"state"`
	}
	path := fmt.Sprintf("control/v1/mutation-leases/%s", leaseID)
	if err := c.do(ctx, c.operationTimeout, http.MethodDelete, path, nil, &result); err != nil {
		return err
	}
	if result.State != "released" {
		return &AdapterError{Code: "invalid_response"}
	}
	return nil
}

func validMutationLease(value MutationLease) bool {
	return value.State == "active" && safeMutationLeaseID(value.LeaseID) && value.ExpiresAt > 0
}

func safeMutationLeaseID(value string) bool {
	if len(value) < 1 || len(value) > 1024 || value == "." || value == ".." || strings.ContainsAny(value, "/\\?#%") {
		return false
	}
	return visibleNonWhitespaceASCII(value, 1, 1024)
}

func visibleNonWhitespaceASCII(value string, minLength, maxLength int) bool {
	if len(value) < minLength || len(value) > maxLength {
		return false
	}
	for _, character := range value {
		if character <= 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func (c *Client) MainAssignment(ctx context.Context) (MainAssignmentStatus, error) {
	var result MainAssignmentStatus
	if err := c.do(ctx, c.readTimeout, http.MethodGet, "control/v1/main/assignment", nil, &result); err != nil {
		return MainAssignmentStatus{}, err
	}
	if !validMainAssignmentStatus(result) {
		return MainAssignmentStatus{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) StageMainAssignment(ctx context.Context, input MainAssignmentRequest) (MainAssignmentStatus, error) {
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	input.ProxyType = strings.ToLower(strings.TrimSpace(input.ProxyType))
	input.ExpectedCurrentCandidateID = strings.TrimSpace(input.ExpectedCurrentCandidateID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.CandidateID == "" || len(input.CandidateID) > 256 ||
		len(input.Country) != 2 || input.Country[0] < 'A' || input.Country[0] > 'Z' || input.Country[1] < 'A' || input.Country[1] > 'Z' ||
		!domainProxyTypeValid(input.ProxyType) || input.ExpectedCurrentCandidateID == "" || len(input.ExpectedCurrentCandidateID) > 256 ||
		len(input.IdempotencyKey) < 8 || len(input.IdempotencyKey) > 256 || strings.IndexFunc(input.IdempotencyKey, func(character rune) bool { return character < 0x21 || character == 0x7f }) >= 0 {
		return MainAssignmentStatus{}, &AdapterError{Code: "invalid_request"}
	}
	var result MainAssignmentStatus
	if err := c.do(ctx, mainAssignmentTimeout, http.MethodPost, "control/v1/main/assign", input, &result); err != nil {
		return MainAssignmentStatus{}, err
	}
	if !validMainAssignmentStatus(result) || result.State != "pending_commit" {
		return MainAssignmentStatus{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) CommitMainAssignment(ctx context.Context, operationID string) (MainAssignmentStatus, error) {
	return c.finishMainAssignment(ctx, operationID, "commit")
}

func (c *Client) RollbackMainAssignment(ctx context.Context, operationID string) (MainAssignmentStatus, error) {
	return c.finishMainAssignment(ctx, operationID, "rollback")
}

func (c *Client) RepairCommitMainAssignment(ctx context.Context, operationID string) (MainAssignmentStatus, error) {
	return c.finishMainAssignment(ctx, operationID, "repair-commit")
}

func (c *Client) RepairReplaceMainAssignment(ctx context.Context, operationID string, input MainRepairRequest) (MainAssignmentStatus, error) {
	operationID = strings.TrimSpace(operationID)
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	input.ProxyType = strings.ToLower(strings.TrimSpace(input.ProxyType))
	if !safeMainOperationID.MatchString(operationID) || input.CandidateID == "" || len(input.CandidateID) > 256 ||
		len(input.Country) != 2 || input.Country[0] < 'A' || input.Country[0] > 'Z' || input.Country[1] < 'A' || input.Country[1] > 'Z' ||
		!domainProxyTypeValid(input.ProxyType) {
		return MainAssignmentStatus{}, &AdapterError{Code: "invalid_request"}
	}
	var result MainAssignmentStatus
	path := fmt.Sprintf("control/v1/main/assign/%s/repair-replace", operationID)
	if err := c.do(ctx, mainAssignmentTimeout, http.MethodPost, path, input, &result); err != nil {
		return MainAssignmentStatus{}, err
	}
	if !validMainAssignmentStatus(result) {
		return MainAssignmentStatus{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) finishMainAssignment(ctx context.Context, operationID, action string) (MainAssignmentStatus, error) {
	operationID = strings.TrimSpace(operationID)
	if !safeMainOperationID.MatchString(operationID) || (action != "commit" && action != "rollback" && action != "repair-commit") {
		return MainAssignmentStatus{}, &AdapterError{Code: "invalid_request"}
	}
	var result MainAssignmentStatus
	path := fmt.Sprintf("control/v1/main/assign/%s/%s", operationID, action)
	if err := c.do(ctx, mainAssignmentTimeout, http.MethodPost, path, struct{}{}, &result); err != nil {
		return MainAssignmentStatus{}, err
	}
	if !validMainAssignmentStatus(result) {
		return MainAssignmentStatus{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func validMainAssignmentStatus(result MainAssignmentStatus) bool {
	if result.State == "idle" {
		return result.OperationID == "" && result.OldCandidateID == "" && result.NewCandidateID == ""
	}
	switch result.State {
	case "switching", "pending_commit", "repairing", "pending_gateway_validation", "committed", "rolling_back", "rolled_back", "repair_required":
	default:
		return false
	}
	if result.Resolution != "" {
		if (result.Resolution != "repair_commit" && result.Resolution != "repair_replace") ||
			(result.State != "pending_gateway_validation" && result.State != "committed") {
			return false
		}
	}
	return safeMainOperationID.MatchString(result.OperationID) &&
		result.OldCandidateID != "" && len(result.OldCandidateID) <= 256 &&
		result.NewCandidateID != "" && len(result.NewCandidateID) <= 256 &&
		len(result.Country) == 2 && result.Country == strings.ToUpper(result.Country) &&
		domainProxyTypeValid(result.ProxyType) && result.Port == 7928 &&
		len(result.ErrorCode) <= 64 && result.ExpiresAt >= 0
}

func domainProxyTypeValid(value string) bool { return value == "residential" || value == "datacenter" }

func (c *Client) GetSlot(ctx context.Context, slot int) (Slot, error) {
	var result Slot
	err := c.do(ctx, c.readTimeout, http.MethodGet, fmt.Sprintf("control/v1/slots/%d", slot), nil, &result)
	return result, err
}

func (c *Client) RotateSlot(ctx context.Context, slot int) (Slot, error) {
	var result Slot
	err := c.do(ctx, c.operationTimeout, http.MethodPost, fmt.Sprintf("control/v1/slots/%d/rotate", slot), struct{}{}, &result)
	return result, err
}

func (c *Client) AssignSlotNode(ctx context.Context, slot int, input AssignSlotRequest) (Slot, error) {
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	input.ProxyType = strings.ToLower(strings.TrimSpace(input.ProxyType))
	if slot < 0 || slot > 255 || input.CandidateID == "" || len(input.CandidateID) > 256 ||
		len(input.Country) != 2 || input.Country[0] < 'A' || input.Country[0] > 'Z' || input.Country[1] < 'A' || input.Country[1] > 'Z' ||
		!domainProxyTypeValid(input.ProxyType) {
		return Slot{}, &AdapterError{Code: "invalid_request"}
	}
	var result Slot
	err := c.do(ctx, c.operationTimeout, http.MethodPost, fmt.Sprintf("control/v1/slots/%d/assign", slot), input, &result)
	return result, err
}

func (c *Client) CheckSlot(ctx context.Context, slot int) (SlotCheck, error) {
	var result SlotCheck
	err := c.do(ctx, c.readTimeout, http.MethodPost, fmt.Sprintf("control/v1/slots/%d/check", slot), struct{}{}, &result)
	return result, err
}

func (c *Client) DeleteSlot(ctx context.Context, slot int) error {
	return c.do(ctx, c.readTimeout, http.MethodDelete, fmt.Sprintf("control/v1/slots/%d", slot), nil, nil)
}

func (c *Client) AdminStatus(ctx context.Context) (AdminStatus, error) {
	var result AdminStatus
	if err := c.do(ctx, c.readTimeout, http.MethodGet, "control/v1/admin", nil, &result); err != nil {
		return AdminStatus{}, err
	}
	if strings.TrimSpace(result.Username) == "" || len(result.Username) > 64 {
		return AdminStatus{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) UpdateAdmin(ctx context.Context, input AdminUpdate) error {
	if strings.TrimSpace(input.Username) == "" || len(input.Password) == 0 {
		return &AdapterError{Code: "invalid_request"}
	}
	wire := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: input.Username, Password: string(input.Password)}
	return c.do(ctx, c.operationTimeout, http.MethodPut, "control/v1/admin", wire, nil)
}

func (c *Client) VerifyAdmin(ctx context.Context, input AdminUpdate) error {
	if strings.TrimSpace(input.Username) == "" || len(input.Password) == 0 {
		return &AdapterError{Code: "invalid_request"}
	}
	wire := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: input.Username, Password: string(input.Password)}
	return c.do(ctx, c.readTimeout, http.MethodPost, "control/v1/admin/verify", wire, nil)
}

func (c *Client) IssueAdminSession(ctx context.Context) (AdminSession, error) {
	var wire struct {
		CookieName   string  `json:"cookieName"`
		SessionToken string  `json:"sessionToken"`
		ExpiresAt    float64 `json:"expiresAt"`
	}
	if err := c.do(ctx, c.readTimeout, http.MethodPost, "control/v1/admin/sessions", struct{}{}, &wire); err != nil {
		return AdminSession{}, err
	}
	if wire.CookieName != "session" || len(wire.SessionToken) < 32 || len(wire.SessionToken) > 1024 || wire.ExpiresAt <= 0 {
		return AdminSession{}, &AdapterError{Code: "invalid_response"}
	}
	for _, character := range wire.SessionToken {
		if character <= 0x20 || character == 0x7f || character == ';' || character == ',' {
			return AdminSession{}, &AdapterError{Code: "invalid_response"}
		}
	}
	return AdminSession{
		CookieName: wire.CookieName,
		Token:      []byte(wire.SessionToken),
		ExpiresAt:  time.Unix(int64(wire.ExpiresAt), 0).UTC(),
	}, nil
}

func (c *Client) do(ctx context.Context, timeout time.Duration, method, path string, input, output any) error {
	return c.doJSON(ctx, timeout, method, path, input, output, false)
}

func (c *Client) doAllowUnknown(ctx context.Context, timeout time.Duration, method, path string, input, output any) error {
	return c.doJSON(ctx, timeout, method, path, input, output, true)
}

func (c *Client) doJSON(ctx context.Context, timeout time.Duration, method, path string, input, output any, allowUnknownData bool) error {
	endpoint, err := url.JoinPath(c.baseURL.String(), path)
	if err != nil {
		return &AdapterError{Code: "invalid_configuration"}
	}
	var body io.Reader
	if input != nil {
		encoded, encodeErr := json.Marshal(input)
		if encodeErr != nil {
			return &AdapterError{Code: "invalid_request"}
		}
		body = bytes.NewReader(encoded)
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, endpoint, body)
	if err != nil {
		return &AdapterError{Code: "invalid_request"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &AdapterError{Code: "timeout"}
		}
		return &AdapterError{Code: "connection_failed"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, controlResponseLimit+1))
	if err != nil || len(raw) > controlResponseLimit {
		return &AdapterError{Code: "invalid_response"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &failure) != nil || failure.Error.Code == "" {
			return &AdapterError{Code: "upstream_rejected"}
		}
		return &AdapterError{Code: failure.Error.Code}
	}
	if output == nil {
		if response.StatusCode != http.StatusNoContent || len(raw) != 0 {
			return &AdapterError{Code: "invalid_response"}
		}
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || len(envelope.Data) == 0 {
		return &AdapterError{Code: "invalid_response"}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return &AdapterError{Code: "invalid_response"}
	}
	dataDecoder := json.NewDecoder(bytes.NewReader(envelope.Data))
	if !allowUnknownData {
		dataDecoder.DisallowUnknownFields()
	}
	if err := dataDecoder.Decode(output); err != nil {
		return &AdapterError{Code: "invalid_response"}
	}
	if err := dataDecoder.Decode(&struct{}{}); err != io.EOF {
		return &AdapterError{Code: "invalid_response"}
	}
	return nil
}
