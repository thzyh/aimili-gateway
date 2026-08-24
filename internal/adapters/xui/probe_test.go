package xui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
)

func TestProbeAcceptsStructuralCSRFResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/panel-fixture/csrf-token" {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"obj":"test-token"}`))
	}))
	t.Cleanup(server.Close)
	checkedAt := time.Unix(1_700_000_000, 0).UTC()
	probe, err := New(server.URL + "/panel-fixture/")
	if err != nil {
		t.Fatal(err)
	}
	probe.now = func() time.Time { return checkedAt }

	result := probe.Probe(context.Background())
	if result.Service != "3x-ui" || result.Health != adapters.HealthHealthy || !result.CheckedAt.Equal(checkedAt) {
		t.Fatalf("unexpected probe result: %#v", result)
	}
	if result.Version != "" || len(result.Capabilities) != 0 || result.ErrorCode != "" {
		t.Fatal("probe exposed unsupported metadata")
	}
}

func TestProbeRejectsRedirectWithoutFollowingIt(t *testing.T) {
	redirectTargetReached := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirect-target" {
			redirectTargetReached = true
			_, _ = response.Write([]byte(`{"success":true,"obj":"test-token"}`))
			return
		}
		http.Redirect(response, request, "/redirect-target", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	probe, err := New(server.URL + "/panel-fixture/")
	if err != nil {
		t.Fatal(err)
	}
	result := probe.Probe(context.Background())
	if redirectTargetReached {
		t.Fatal("probe followed a redirect")
	}
	if result.Health != adapters.HealthDegraded || result.ErrorCode != "unexpected_status" {
		t.Fatalf("health=%q errorCode=%q", result.Health, result.ErrorCode)
	}
}

func TestProbeRejectsMalformedCSRFResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"success":true,"obj":`))
	}))
	t.Cleanup(server.Close)
	probe, err := New(server.URL + "/panel-fixture/")
	if err != nil {
		t.Fatal(err)
	}
	result := probe.Probe(context.Background())
	if result.Health != adapters.HealthDegraded || result.ErrorCode != "invalid_response" {
		t.Fatalf("health=%q errorCode=%q", result.Health, result.ErrorCode)
	}
}

func TestProbeMapsTimeoutWithoutExposingBasePath(t *testing.T) {
	probe, err := New("http://127.0.0.1:2001/panel-fixture/")
	if err != nil {
		t.Fatal(err)
	}
	probe.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	result := probe.Probe(context.Background())
	if result.Health != adapters.HealthUnavailable || result.ErrorCode != "timeout" {
		t.Fatalf("health=%q errorCode=%q", result.Health, result.ErrorCode)
	}
	if strings.Contains(result.ErrorCode, "panel-fixture") {
		t.Fatal("probe exposed configured base path")
	}
}

func TestProbeRedactsUnexpectedTransportError(t *testing.T) {
	probe, err := New("http://127.0.0.1:2001/panel-fixture/")
	if err != nil {
		t.Fatal(err)
	}
	probe.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("sensitive transport detail")
	})
	result := probe.Probe(context.Background())
	if strings.Contains(result.ErrorCode, "sensitive") {
		t.Fatal("raw transport error exposed")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
