package xui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
)

const (
	probeTimeout  = 2 * time.Second
	responseLimit = 16 << 10
)

type Probe struct {
	endpoint string
	client   *http.Client
	now      func() time.Time
}

func New(baseURL string) (*Probe, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid 3x-ui base URL")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("3x-ui host must be loopback")
		}
	}
	endpoint, err := url.JoinPath(baseURL, "csrf-token")
	if err != nil {
		return nil, errors.New("invalid 3x-ui base URL")
	}
	return &Probe{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: probeTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}, nil
}

func (p *Probe) Probe(ctx context.Context) adapters.ProbeResult {
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeContext, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return p.result(adapters.HealthUnavailable, "invalid_configuration")
	}
	request.Header.Set("Accept", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		code := "connection_failed"
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			code = "timeout"
		}
		return p.result(adapters.HealthUnavailable, code)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseLimit))
		return p.result(adapters.HealthDegraded, "unexpected_status")
	}
	var payload struct {
		Success bool   `json:"success"`
		Object  string `json:"obj"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, responseLimit))
	if err := decoder.Decode(&payload); err != nil || !payload.Success || payload.Object == "" {
		return p.result(adapters.HealthDegraded, "invalid_response")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return p.result(adapters.HealthDegraded, "invalid_response")
	}
	return p.result(adapters.HealthHealthy, "")
}

func (p *Probe) result(health adapters.Health, errorCode string) adapters.ProbeResult {
	return adapters.ProbeResult{
		Service:      "3x-ui",
		Health:       health,
		Capabilities: []string{},
		ErrorCode:    errorCode,
		CheckedAt:    p.now().UTC(),
	}
}
