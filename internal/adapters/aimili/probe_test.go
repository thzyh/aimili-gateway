package aimili

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
)

func TestProbeReportsReachableListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	checkedAt := time.Unix(1_700_000_000, 0).UTC()
	probe := New(listener.Addr().String())
	probe.now = func() time.Time { return checkedAt }

	result := probe.Probe(context.Background())
	if result.Service != "aimili-vpn" || result.Health != adapters.HealthHealthy || !result.CheckedAt.Equal(checkedAt) {
		t.Fatalf("unexpected probe result: %#v", result)
	}
	if result.Version != "" || len(result.Capabilities) != 0 || result.ErrorCode != "" {
		t.Fatal("reachability probe exposed unsupported metadata")
	}
}

func TestProbeReportsRefusedListenerWithoutRawError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	result := New(address).Probe(context.Background())
	if result.Health != adapters.HealthUnavailable || result.ErrorCode != "connection_failed" {
		t.Fatalf("health=%q errorCode=%q", result.Health, result.ErrorCode)
	}
	if strings.Contains(result.ErrorCode, address) {
		t.Fatal("probe result exposed configured address")
	}
}

func TestProbeMapsTimeoutToStableErrorCode(t *testing.T) {
	probe := New("127.0.0.1:8787")
	probe.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 1100*time.Millisecond {
			t.Fatal("one-second probe deadline missing")
		}
		return nil, context.DeadlineExceeded
	}
	result := probe.Probe(context.Background())
	if result.Health != adapters.HealthUnavailable || result.ErrorCode != "timeout" {
		t.Fatalf("health=%q errorCode=%q", result.Health, result.ErrorCode)
	}
}

func TestProbeRedactsUnexpectedDialError(t *testing.T) {
	probe := New("127.0.0.1:8787")
	probe.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("sensitive dial detail")
	}
	result := probe.Probe(context.Background())
	if strings.Contains(result.ErrorCode, "sensitive") {
		t.Fatal("raw dial error exposed")
	}
}
