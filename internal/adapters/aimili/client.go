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
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/securefile"
)

const (
	controlReadTimeout      = 8 * time.Second
	controlOperationTimeout = 75 * time.Second
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
	Code           string  `json:"code"`
	Name           string  `json:"name"`
	CandidateCount int     `json:"candidateCount"`
	ObservedAt     float64 `json:"observedAt"`
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
}

type CreateSlotRequest struct {
	Country     string `json:"country"`
	ProxyType   string `json:"proxyType"`
	CandidateID string `json:"candidateId,omitempty"`
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
		if len(country.Code) != 2 || country.Code != strings.ToUpper(country.Code) || country.CandidateCount < 0 || country.ObservedAt < 0 {
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
		refresh.ValidCount >= 0 && refresh.PreservedCount >= 0 && refresh.StartedAt >= 0 && refresh.FinishedAt >= 0
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
