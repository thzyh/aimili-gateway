package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thzyh/aimili-gateway/internal/app"
	"github.com/thzyh/aimili-gateway/internal/config"
)

func main() {
	cfg, err := config.Load(os.Getenv("GATEWAY_CONFIG"))
	if err != nil {
		log.Fatalf("load gateway configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg)
	if err != nil {
		log.Fatalf("initialize gateway: %v", err)
	}
	server := newHTTPServer(cfg, application.Handler())
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("gateway shutdown: %v", err)
		}
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve gateway: %v", err)
	}
	if err := application.Close(); err != nil {
		log.Printf("close gateway: %v", err)
	}
}

func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
}
