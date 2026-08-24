package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
