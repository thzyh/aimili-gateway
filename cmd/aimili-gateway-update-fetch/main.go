package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/updatefetch"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: aimili-gateway-update-fetch <fetch|spool>")
		return 2
	}
	if args[0] == "spool" {
		return runSpool(args[1:], stdout, stderr)
	}
	if args[0] != "fetch" {
		fmt.Fprintln(stderr, "unknown command")
		return 2
	}
	flags := flag.NewFlagSet("fetch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "updater configuration")
	requestPath := flags.String("request", "", "update request")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *configPath == "" || *requestPath == "" {
		if err == nil {
			fmt.Fprintln(stderr, "config and request are required")
		}
		return 2
	}
	config, err := updatefetch.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "invalid_config")
		return 1
	}
	request, err := updatetxn.ReadRequestFile(*requestPath)
	if err != nil {
		fmt.Fprintln(stderr, "invalid_request")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := (&updatefetch.Fetcher{Config: config}).Fetch(ctx, request)
	if err != nil {
		code := updatefetch.ErrorCode(err)
		if code == "" {
			code = "fetch_failed"
		}
		fmt.Fprintln(stderr, code)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode_result_failed")
		return 1
	}
	return 0
}

func runSpool(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("spool", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "updater configuration")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *configPath == "" {
		if err == nil {
			fmt.Fprintln(stderr, "config is required")
		}
		return 2
	}
	config, err := updatefetch.LoadConfig(*configPath)
	if err != nil || config.ValidateSpool() != nil {
		fmt.Fprintln(stderr, "invalid_config")
		return 1
	}
	entries, err := os.ReadDir(config.RequestDir)
	if err != nil {
		fmt.Fprintln(stderr, "read_requests_failed")
		return 1
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		request, err := updatetxn.ReadRequestFile(filepath.Join(config.RequestDir, entry.Name()))
		if err != nil {
			continue
		}
		if _, err := os.Lstat(filepath.Join(config.StagingRoot, request.RunID, "download.complete")); err == nil {
			continue
		}
		if request.Action == updatetxn.ActionRollback {
			if err := writeFetchMarker(config.StagingRoot, request.RunID, "", true); err != nil {
				fmt.Fprintln(stderr, "staging_failed")
				return 1
			}
			return 0
		}
		_, fetchErr := (&updatefetch.Fetcher{Config: config}).Fetch(context.Background(), request)
		if fetchErr != nil {
			code := updatefetch.ErrorCode(fetchErr)
			if code == "" {
				code = "fetch_failed"
			}
			if err := writeFetchMarker(config.StagingRoot, request.RunID, code, false); err != nil {
				fmt.Fprintln(stderr, "staging_failed")
				return 1
			}
		}
		return 0
	}
	return 0
}

func writeFetchMarker(stagingRoot, runID, errorCode string, rollback bool) error {
	directory := filepath.Join(stagingRoot, runID)
	if err := os.Mkdir(directory, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	body, err := json.Marshal(struct {
		RunID     string `json:"runId"`
		ErrorCode string `json:"errorCode,omitempty"`
		Rollback  bool   `json:"rollback,omitempty"`
	}{RunID: runID, ErrorCode: errorCode, Rollback: rollback})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "download.complete"), body, 0o600)
}
