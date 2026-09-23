package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thzyh/aimili-gateway/internal/app"
	"github.com/thzyh/aimili-gateway/internal/buildinfo"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		if err := serve(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if len(args) == 2 && args[0] == "version" && args[1] == "--json" {
		if err := json.NewEncoder(stdout).Encode(buildinfo.Current()); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if len(args) == 2 && args[0] == "config" && args[1] == "validate" {
		if _, err := config.Load(os.Getenv("GATEWAY_CONFIG")); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "ok")
		return 0
	}
	if len(args) >= 2 && args[0] == "database" && args[1] == "check-compatible" {
		flags := flag.NewFlagSet("database check-compatible", flag.ContinueOnError)
		flags.SetOutput(stderr)
		minimum := flags.Int("min", 0, "minimum compatible schema")
		maximum := flags.Int("max", 0, "maximum compatible schema")
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 || *minimum < 1 || *maximum < *minimum {
			if err == nil {
				fmt.Fprintln(stderr, "valid --min and --max are required")
			}
			return 2
		}
		cfg, err := config.Load(os.Getenv("GATEWAY_CONFIG"))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		database, err := store.OpenReadOnly(context.Background(), cfg.DatabasePath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer database.Close()
		version, err := database.SchemaVersion(context.Background())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		compatible := version >= *minimum && version <= *maximum
		if err := json.NewEncoder(stdout).Encode(struct {
			SchemaVersion int  `json:"schemaVersion"`
			Compatible    bool `json:"compatible"`
		}{SchemaVersion: version, Compatible: compatible}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !compatible {
			return 1
		}
		return 0
	}
	fmt.Fprintln(stderr, "unknown command")
	return 2
}

func serve() error {
	cfg, err := config.Load(os.Getenv("GATEWAY_CONFIG"))
	if err != nil {
		return fmt.Errorf("load gateway configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initialize gateway: %w", err)
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
		return fmt.Errorf("serve gateway: %w", err)
	}
	if err := application.Close(); err != nil {
		return fmt.Errorf("close gateway: %w", err)
	}
	return nil
}

func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      max(5*time.Minute, time.Duration(cfg.ProtocolTimeoutSeconds)*time.Second+2*time.Minute),
		IdleTimeout:       60 * time.Second,
	}
}
