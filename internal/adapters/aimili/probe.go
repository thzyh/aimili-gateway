package aimili

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
)

const probeTimeout = time.Second

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type Probe struct {
	address     string
	dialContext dialContextFunc
	now         func() time.Time
}

func New(address string) *Probe {
	dialer := &net.Dialer{}
	return &Probe{
		address:     address,
		dialContext: dialer.DialContext,
		now:         time.Now,
	}
}

func (p *Probe) Probe(ctx context.Context) adapters.ProbeResult {
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	connection, err := p.dialContext(probeContext, "tcp", p.address)
	if err != nil {
		code := "connection_failed"
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			code = "timeout"
		}
		return p.result(adapters.HealthUnavailable, code)
	}
	_ = connection.Close()
	return p.result(adapters.HealthHealthy, "")
}

func (p *Probe) result(health adapters.Health, errorCode string) adapters.ProbeResult {
	return adapters.ProbeResult{
		Service:      "aimili-vpn",
		Health:       health,
		Capabilities: []string{},
		ErrorCode:    errorCode,
		CheckedAt:    p.now().UTC(),
	}
}
