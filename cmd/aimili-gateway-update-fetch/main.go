package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/thzyh/aimili-gateway/internal/updatefetch"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "fetch" {
		fmt.Fprintln(stderr, "usage: aimili-gateway-update-fetch fetch --config FILE --request FILE")
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
	request, err := readRequest(*requestPath)
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

func readRequest(filename string) (updatetxn.Request, error) {
	body, err := os.ReadFile(filename)
	if err != nil || len(body) > 32<<10 {
		return updatetxn.Request{}, errors.New("request unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request updatetxn.Request
	if err := decoder.Decode(&request); err != nil {
		return updatetxn.Request{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return updatetxn.Request{}, errors.New("trailing request data")
	}
	if err := updatetxn.ValidateRequest(request); err != nil {
		return updatetxn.Request{}, err
	}
	return request, nil
}
