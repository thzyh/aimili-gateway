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
	if *configPath != installerConfigPath {
		fmt.Fprintln(stderr, "invalid_config")
		return 2
	}
	config, err := updatefetch.LoadConfig(*configPath)
	if err != nil || config.ValidateSpool() != nil {
		fmt.Fprintln(stderr, "invalid_config")
		return 1
	}
	return runInstaller(config, nil, stdout, stderr)
}

func runInstaller(config updatefetch.Config, direct *updatetxn.Request, stdout, stderr io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "root_required")
		return 1
	}
	return runInstallerAt(updatetxn.InstallerStateRoot, config, direct, stdout, stderr)
}

func runInstallerAt(stateRoot string, config updatefetch.Config, direct *updatetxn.Request, stdout, stderr io.Writer) int {
	release, err := updatetxn.AcquireExecutionLease(stateRoot)
	if err != nil {
		fmt.Fprintln(stderr, "update_busy")
		return 1
	}
	defer release()
	if journal, err := updatetxn.ReadJournal(stateRoot); err == nil {
		request := journal.Request
		result := processStagedRequestAt(stateRoot, request, filepath.Join(config.StagingRoot, request.RunID), config)
		return finishRequest(stateRoot, config, request, result, stdout, stderr)
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, "repair_required")
		return 1
	}
	if direct != nil {
		if existing, err := trustedTerminal(config, *direct); err == nil {
			return finishRequest(stateRoot, config, *direct, existing, stdout, stderr)
		}
		result := processStagedRequestAt(stateRoot, *direct, filepath.Join(config.StagingRoot, direct.RunID), config)
		return finishRequest(stateRoot, config, *direct, result, stdout, stderr)
	}
	entries, err := os.ReadDir(config.RequestDir)
	if err != nil {
		fmt.Fprintln(stderr, "read_staging_failed")
		return 1
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".json")
		staging := filepath.Join(config.StagingRoot, runID)
		request, err := updatetxn.ReadRequestFile(filepath.Join(config.RequestDir, runID+".json"))
		if err != nil || request.RunID != runID {
			continue
		}
		if existing, err := trustedTerminal(config, request); err == nil {
			return finishRequest(stateRoot, config, request, existing, stdout, stderr)
		}
		if _, err := os.Lstat(filepath.Join(staging, "download.complete")); err != nil {
			continue
		}
		result := processStagedRequestAt(stateRoot, request, staging, config)
		return finishRequest(stateRoot, config, request, result, stdout, stderr)
	}
	return 0
}

func trustedTerminal(config updatefetch.Config, request updatetxn.Request) (updatetxn.Result, error) {
	client := updatetxn.Client{RequestDir: config.RequestDir, ResultDir: config.ResultDir}
	if uid := os.Geteuid(); uid >= 0 {
		value := uint32(uid)
		client.TrustedResultUID = &value
	}
	result, err := client.Get(context.Background(), request.RunID)
	if err != nil || !result.State.Terminal() || result.Kind != request.Kind {
		return result, updatetxn.ErrUntrustedResult
	}
	return result, nil
}

func finishRequest(stateRoot string, config updatefetch.Config, request updatetxn.Request, result updatetxn.Result, stdout, stderr io.Writer) int {
	if !result.State.Terminal() {
		fmt.Fprintln(stderr, "repair_required")
		return 1
	}
	if result.FinishedAt == nil {
		finished := time.Now().UTC()
		result.FinishedAt = &finished
	}
	if err := updatetxn.WriteResultFile(config.ResultDir, result); err != nil {
		if !errors.Is(err, os.ErrExist) {
			fmt.Fprintln(stderr, "write_result_failed")
			return 1
		}
		existing, readErr := trustedTerminal(config, request)
		if readErr != nil || existing.State != result.State {
			fmt.Fprintln(stderr, "repair_required")
			return 1
		}
	}
	// Exact request consumption follows durable terminal publication even for
	// repair_required; diagnostic staging and journal remain in that case.
	if err := updatetxn.RemoveRequest(config.RequestDir, request.RunID); err != nil {
		fmt.Fprintln(stderr, "cleanup_failed")
		return 1
	}
	if result.State != updatetxn.StateRepairRequired {
		if err := os.RemoveAll(filepath.Join(config.StagingRoot, request.RunID)); err != nil {
			fmt.Fprintln(stderr, "cleanup_failed")
			return 1
		}
		if err := updatetxn.ClearJournal(stateRoot, request.RunID); err != nil {
			fmt.Fprintln(stderr, "cleanup_failed")
			return 1
		}
		if request.Kind == updatetxn.KindUI && (result.State == updatetxn.StateSuccess || result.State == updatetxn.StateRolledBack) {
			if err := uirelease.Cleanup(config.UIRoot); err != nil {
				fmt.Fprintln(stderr, "cleanup_failed")
				return 1
			}
		}
	}
	if err := updatetxn.PruneResults(config.ResultDir, config.RequestDir, request.RunID, 64); err != nil {
		fmt.Fprintln(stderr, "cleanup_failed")
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode_result_failed")
		return 1
	}
	return 0
}

func processStagedRequest(request updatetxn.Request, staging string, config updatefetch.Config) updatetxn.Result {
	return processStagedRequestAt(updatetxn.InstallerStateRoot, request, staging, config)
}

func processStagedRequestAt(stateRoot string, request updatetxn.Request, staging string, config updatefetch.Config) updatetxn.Result {
	started := request.RequestedAt
	base := updatetxn.Result{RunID: request.RunID, Kind: request.Kind, Version: request.Version, StartedAt: started}
	_, journalErr := updatetxn.ReadJournal(stateRoot)
	if code := stagedFetchError(filepath.Join(staging, "download.complete")); code != "" && errors.Is(journalErr, os.ErrNotExist) && request.Action == updatetxn.ActionApply {
		finished := time.Now().UTC()
		base.State, base.ErrorCode, base.FinishedAt = updatetxn.StateFailed, code, &finished
		return base
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if request.Kind == updatetxn.KindUI {
		return processUIRequestAt(ctx, stateRoot, base, request, staging, config)
	}
	trustedUID := config.FetcherUID
	probe := &productionProbe{databasePath: config.DatabasePath, xuiDatabasePath: config.XUIDatabasePath, healthURL: config.HealthURL}
	gatewayConfig := gatewayupdate.Config{
		StateDir: stateRoot, Request: request,
		RunID: request.RunID, StagingDir: staging, BinaryPath: config.BinaryPath, PreviousPath: config.PreviousPath,
		ConfigPath: config.GatewayConfigPath, DatabasePath: config.DatabasePath, PublicKeyFile: config.PublicKeyFile,
		APIVersion: "v1", Platform: "linux-amd64", AllowInstall: config.AllowGatewayInstall,
		TrustedStagingUID: &trustedUID, Runner: systemdRunner{}, Probe: probe, BusyCheck: databaseBusyCheck(config.DatabasePath),
	}
	var installed gatewayupdate.Result
	var err error
	if recovered, exists, recoveryErr := gatewayupdate.Recover(ctx, gatewayConfig); exists || recoveryErr != nil {
		installed, err = recovered, recoveryErr
	} else if request.Action == updatetxn.ActionRollback {
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
	return processUIRequestAt(ctx, updatetxn.InstallerStateRoot, base, request, staging, config)
}

func processUIRequestAt(ctx context.Context, stateRoot string, base updatetxn.Result, request updatetxn.Request, staging string, config updatefetch.Config) updatetxn.Result {
	uiConfig := uirelease.Config{
		StateDir: stateRoot, Request: request,
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
		if base.ErrorCode == "repair_required" {
			base.State = updatetxn.StateRepairRequired
		}
		return base
	}
	base.State = updatetxn.State(installed.State)
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
		return "fetch_failed"
	}
	if marker.ErrorCode == "" {
		return ""
	}
	if !safeFetchError.MatchString(marker.ErrorCode) {
		return "fetch_failed"
	}
	return marker.ErrorCode
}
