package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/config"
)

func TestNewHTTPServerUsesApplicationHandler(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called = true
		response.WriteHeader(http.StatusAccepted)
	})
	server := newHTTPServer(config.Config{ListenAddress: "127.0.0.1:9080"}, handler)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusAccepted {
		t.Fatal("application handler was not used")
	}
	if server.Addr != "127.0.0.1:9080" {
		t.Fatalf("server address = %q", server.Addr)
	}
}

func TestNewHTTPServerAllowsLongProxyProvisioning(t *testing.T) {
	server := newHTTPServer(config.Config{ListenAddress: "127.0.0.1:9080"}, http.NotFoundHandler())
	if server.WriteTimeout < 2*time.Minute {
		t.Fatalf("write timeout %s cannot cover Aimili provisioning and protocol validation", server.WriteTimeout)
	}
}
