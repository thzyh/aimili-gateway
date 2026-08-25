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
	"runtime"
	"strings"
	"time"
)

const (
	controlTimeout       = 8 * time.Second
	controlResponseLimit = 16 << 10
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

type CreateSlotRequest struct {
	Country   string `json:"country"`
	ProxyType string `json:"proxyType"`
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
	LatencyMS   int     `json:"latency_ms"`
	CheckedAt   float64 `json:"checked_at"`
}

type SlotCheck = Slot

type AdapterError struct {
	Code string
}

func (e *AdapterError) Error() string {
	return "aimili control request failed: " + e.Code
}

type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
}

func ReadTokenFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("open Aimili control token file")
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 4096 {
		return nil, errors.New("invalid Aimili control token file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
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
		baseURL: parsed,
		token:   secret,
		httpClient: &http.Client{
			Timeout: controlTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var result Capabilities
	err := c.do(ctx, http.MethodGet, "control/v1/capabilities", nil, &result)
	return result, err
}

func (c *Client) Candidates(ctx context.Context) ([]Candidate, error) {
	var result []Candidate
	err := c.do(ctx, http.MethodGet, "control/v1/candidates", nil, &result)
	return result, err
}

func (c *Client) CreateSlot(ctx context.Context, input CreateSlotRequest) (Slot, error) {
	var result Slot
	err := c.do(ctx, http.MethodPost, "control/v1/slots", input, &result)
	return result, err
}

func (c *Client) GetSlot(ctx context.Context, slot int) (Slot, error) {
	var result Slot
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("control/v1/slots/%d", slot), nil, &result)
	return result, err
}

func (c *Client) RotateSlot(ctx context.Context, slot int) (Slot, error) {
	var result Slot
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("control/v1/slots/%d/rotate", slot), struct{}{}, &result)
	return result, err
}

func (c *Client) CheckSlot(ctx context.Context, slot int) (SlotCheck, error) {
	var result SlotCheck
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("control/v1/slots/%d/check", slot), struct{}{}, &result)
	return result, err
}

func (c *Client) DeleteSlot(ctx context.Context, slot int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("control/v1/slots/%d", slot), nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
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
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
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
	dataDecoder.DisallowUnknownFields()
	if err := dataDecoder.Decode(output); err != nil {
		return &AdapterError{Code: "invalid_response"}
	}
	if err := dataDecoder.Decode(&struct{}{}); err != io.EOF {
		return &AdapterError{Code: "invalid_response"}
	}
	return nil
}
