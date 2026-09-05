package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/gatewayupdate"
	"github.com/thzyh/aimili-gateway/internal/uirelease"
	"github.com/thzyh/aimili-gateway/internal/updatefetch"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

var safeFetchError = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func runInstallSpool(args []string, stdout, stderr io.Writer) int {
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
	entries, err := os.ReadDir(config.StagingRoot)
	if err != nil {
		fmt.Fprintln(stderr, "read_staging_failed")
		return 1
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 64 {
			continue
		}
		runID := entry.Name()
		staging := filepath.Join(config.StagingRoot, runID)
		if _, err := os.Lstat(filepath.Join(staging, "download.complete")); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(config.ResultDir, runID+".json")); err == nil {
			_ = os.RemoveAll(staging)
			continue
		}
		request, err := updatetxn.ReadRequestFile(filepath.Join(config.RequestDir, runID+".json"))
		if err != nil || request.RunID != runID {
			continue
		}
		result := processStagedRequest(request, staging, config)
		if result.FinishedAt == nil {
			finished := time.Now().UTC()
			result.FinishedAt = &finished
		}
		if err := updatetxn.WriteResultFile(config.ResultDir, result); err != nil && !errors.Is(err, os.ErrExist) {
			fmt.Fprintln(stderr, "write_result_failed")
			return 1
		}
		if result.State != updatetxn.StateRepairRequired {
			_ = os.RemoveAll(staging)
		}
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintln(stderr, "encode_result_failed")
			return 1
		}
		return 0
	}
	return 0
}

func processStagedRequest(request updatetxn.Request, staging string, config updatefetch.Config) updatetxn.Result {
	started := request.RequestedAt
	base := updatetxn.Result{RunID: request.RunID, Kind: request.Kind, Version: request.Version, StartedAt: started}
	if code := stagedFetchError(filepath.Join(staging, "download.complete")); code != "" {
		finished := time.Now().UTC()
		base.State, base.ErrorCode, base.FinishedAt = updatetxn.StateFailed, code, &finished
		return base
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if request.Kind == updatetxn.KindUI {
		return processUIRequest(ctx, base, request, staging, config)
	}
	trustedUID := config.FetcherUID
	probe := &productionProbe{databasePath: config.DatabasePath, healthURL: config.HealthURL}
	gatewayConfig := gatewayupdate.Config{
		RunID: request.RunID, StagingDir: staging, BinaryPath: config.BinaryPath, PreviousPath: config.PreviousPath,
		ConfigPath: config.GatewayConfigPath, DatabasePath: config.DatabasePath, PublicKeyFile: config.PublicKeyFile,
		APIVersion: "v1", Platform: "linux-amd64", AllowInstall: config.AllowGatewayInstall,
		TrustedStagingUID: &trustedUID, Runner: systemdRunner{}, Probe: probe, BusyCheck: databaseBusyCheck(config.DatabasePath),
	}
	var installed gatewayupdate.Result
	var err error
	if request.Action == updatetxn.ActionRollback {
		installed, err = gatewayupdate.Rollback(ctx, gatewayConfig)
	} else if request.DryRun {
		installed, err = gatewayupdate.DryRun(ctx, gatewayConfig)
	} else {
		installed, err = gatewayupdate.Install(ctx, gatewayConfig)
	}
	finished := installed.FinishedAt
	base.State, base.ErrorCode, base.FinishedAt = installed.State, installed.ErrorCode, &finished
	if err != nil && base.ErrorCode == "" {
		base.ErrorCode = gatewayupdate.ErrorCode(err)
	}
	return base
}

func processUIRequest(ctx context.Context, base updatetxn.Result, request updatetxn.Request, staging string, config updatefetch.Config) updatetxn.Result {
	_ = os.Remove(filepath.Join(staging, "download.complete"))
	uiConfig := uirelease.Config{
		StagingDir: staging, Root: config.UIRoot, PublicKeyFile: config.PublicKeyFile, APIVersion: "v1",
		AvailableBytes: uirelease.AvailableBytes, HealthCheck: gatewayHealthCheck(strings.TrimSuffix(config.HealthURL, "/healthz")),
	}
	var installed uirelease.Result
	var err error
	if request.Action == updatetxn.ActionRollback {
		installed, err = uirelease.Rollback(ctx, uiConfig)
	} else {
		installed, err = uirelease.Install(ctx, uiConfig)
	}
	finished := time.Now().UTC()
	base.FinishedAt = &finished
	if err != nil {
		base.State = updatetxn.StateFailed
		base.ErrorCode = uirelease.ErrorCode(err)
		if base.ErrorCode == "" {
			base.ErrorCode = "operation_failed"
		}
		return base
	}
	base.State = updatetxn.StateSuccess
	base.Version = installed.Version
	return base
}

func stagedFetchError(filename string) string {
	body, err := os.ReadFile(filename)
	if err != nil || len(body) > 32<<10 {
		return "fetch_failed"
	}
	var marker struct {
		ErrorCode string `json:"errorCode"`
	}
	if json.Unmarshal(body, &marker) != nil {
		return ""
	}
	if marker.ErrorCode == "" {
		return ""
	}
	if !safeFetchError.MatchString(marker.ErrorCode) {
		return "fetch_failed"
	}
	return marker.ErrorCode
}
