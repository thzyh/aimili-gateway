package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/uirelease"
)

var assetPattern = regexp.MustCompile(`(?:src|href)=["'](/assets/[^"'?#]+)`)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: aimili-gateway-update-install <ui-install|ui-rollback|gateway-dry-run|gateway-install|gateway-rollback>")
		return 2
	}
	command := args[0]
	if command == "spool" {
		return runInstallSpool(args[1:], stdout, stderr)
	}
	if strings.HasPrefix(command, "gateway-") {
		return runGatewayCommand(command, args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "", "external UI root")
	staging := flags.String("staging", "", "release staging directory")
	publicKey := flags.String("public-key", "", "Ed25519 public key file")
	healthURL := flags.String("health-url", "http://127.0.0.1:9080", "Gateway loopback URL")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *root == "" {
		fmt.Fprintln(stderr, "root is required")
		return 2
	}
	if command == "ui-install" && (*staging == "" || *publicKey == "") {
		fmt.Fprintln(stderr, "staging and public-key are required")
		return 2
	}
	if command != "ui-install" && command != "ui-rollback" {
		fmt.Fprintln(stderr, "unknown command")
		return 2
	}
	if err := validateLoopbackURL(*healthURL); err != nil {
		fmt.Fprintln(stderr, "health-url must use a loopback host")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	config := uirelease.Config{
		StagingDir: *staging, Root: *root, PublicKeyFile: *publicKey, APIVersion: "v1",
		AvailableBytes: uirelease.AvailableBytes,
		HealthCheck:    gatewayHealthCheck(*healthURL),
	}
	var result uirelease.Result
	var err error
	if command == "ui-install" {
		result, err = uirelease.Install(ctx, config)
	} else {
		result, err = uirelease.Rollback(ctx, config)
	}
	if err != nil {
		code := uirelease.ErrorCode(err)
		if code == "" {
			code = "operation_failed"
		}
		if detail := healthDetail(err); detail != "" {
			code += ":" + detail
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

type healthError struct {
	detail string
	err    error
}

func (e *healthError) Error() string { return e.detail }
func (e *healthError) Unwrap() error { return e.err }

func healthDetail(err error) string {
	var health *healthError
	if errors.As(err, &health) {
		return health.detail
	}
	return ""
}

func validateLoopbackURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return errors.New("invalid health URL")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("health URL is not loopback")
		}
	}
	return nil
}

func gatewayHealthCheck(base string) func(context.Context, string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context, version string) error {
		manifest, err := fetch(ctx, client, base+"/manifest.json")
		if err != nil {
			return &healthError{detail: "manifest_unavailable", err: err}
		}
		if !strings.Contains(string(manifest), `"version":"`+version+`"`) {
			return &healthError{detail: "manifest_version_mismatch"}
		}
		index, err := fetch(ctx, client, base+"/")
		if err != nil {
			return &healthError{detail: "index_unavailable", err: err}
		}
		for _, match := range assetPattern.FindAllSubmatch(index, -1) {
			if _, err := fetch(ctx, client, base+string(match[1])); err != nil {
				return &healthError{detail: "entry_asset_unavailable", err: err}
			}
		}
		return nil
	}
}

func fetch(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}
