package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/updatefetch"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

const installerConfigPath = "/etc/aimili-gateway/updater.json"
const installerService = "aimili-gateway-update-install.service"

// Direct operations use exactly the service configuration and lease, never a
// caller-supplied binary, staging path, URL, public key, or shell command.
func runDirectCommand(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	runID := flags.String("run-id", "", "update run identifier")
	version := flags.String("version", "", "signed release identifier")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	request := updatetxn.Request{RunID: *runID, Version: *version, Action: updatetxn.ActionApply, RequestedAt: time.Now().UTC()}
	if strings.HasPrefix(command, "ui-") {
		request.Kind = updatetxn.KindUI
	} else {
		request.Kind = updatetxn.KindGateway
	}
	switch command {
	case "ui-install", "gateway-install":
	case "gateway-dry-run":
		request.DryRun = true
	case "ui-rollback", "gateway-rollback":
		request.Action = updatetxn.ActionRollback
	default:
		fmt.Fprintln(stderr, "unknown command")
		return 2
	}
	if updatetxn.ValidateRequest(request) != nil {
		fmt.Fprintln(stderr, "run-id and signed version are required for apply")
		return 2
	}
	config, err := updatefetch.LoadConfig(installerConfigPath)
	if err != nil || config.ValidateSpool() != nil {
		fmt.Fprintln(stderr, "invalid_config")
		return 1
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "root_required")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := updatetxn.Client{RequestDir: config.RequestDir, ResultDir: config.ResultDir}
	uid := uint32(0)
	client.TrustedResultUID = &uid
	if result, err := client.Get(ctx, request.RunID); err == nil && result.State.Terminal() {
		return printDirectResult(result, stdout, stderr)
	}
	if _, err := client.Submit(ctx, request); err != nil {
		fmt.Fprintln(stderr, "update_busy")
		return 1
	}
	if request.Action == updatetxn.ActionRollback {
		staging := filepath.Join(config.StagingRoot, request.RunID)
		if err := os.Mkdir(staging, 0700); err != nil && !os.IsExist(err) {
			fmt.Fprintln(stderr, "staging_failed")
			return 1
		}
		file, err := os.OpenFile(filepath.Join(staging, "download.complete"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_, err = file.Write([]byte(`{"rollback":true}`))
			if err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil && !os.IsExist(err) {
			fmt.Fprintln(stderr, "staging_failed")
			return 1
		}
	}
	// Never execute verified candidate code in the invoking shell namespace.
	// systemctl waits for the fixed oneshot; it does not start the fetcher.
	if err := exec.CommandContext(ctx, "/usr/bin/systemctl", "start", installerService).Run(); err != nil {
		fmt.Fprintln(stderr, "installer_start_failed")
		return 1
	}
	result, err := client.Get(ctx, request.RunID)
	if err != nil || !result.State.Terminal() {
		fmt.Fprintln(stderr, "update_pending")
		return 1
	}
	return printDirectResult(result, stdout, stderr)
}

func printDirectResult(result updatetxn.Result, stdout, stderr io.Writer) int {
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode_result_failed")
		return 1
	}
	if result.State == updatetxn.StateFailed || result.State == updatetxn.StateRepairRequired {
		return 1
	}
	return 0
}

func inInstallerService() bool {
	group, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	member := false
	for _, line := range strings.Split(string(group), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) == 3 && strings.HasSuffix(fields[2], "/"+installerService) {
			member = true
		}
	}
	if !member {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", installerService).Output()
	return err == nil && strings.TrimSpace(string(body)) == strconv.Itoa(os.Getpid())
}
