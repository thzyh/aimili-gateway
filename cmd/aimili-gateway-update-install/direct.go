package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/updatefetch"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

const installerConfigPath = "/etc/aimili-gateway/updater.json"

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
	return runInstaller(config, &request, stdout, stderr)
}
