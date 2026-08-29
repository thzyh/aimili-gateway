package validator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type VLESSTarget struct {
	XrayPath       string
	TempDirectory  string
	InboundAddress string
	ClientID       string
	PublicKey      string
	ShortID        string
	ServerName     string
	ProbeHost      string
	ExpectedExitIP string
	MLDSA65Verify  string
}

func (v *Validator) ValidateVLESS(ctx context.Context, target VLESSTarget) (Result, error) {
	if v == nil || target.XrayPath == "" || target.ProbeHost == "" || net.ParseIP(target.ExpectedExitIP) == nil {
		return Result{}, validationFailure("invalid_configuration")
	}
	info, err := os.Stat(target.XrayPath)
	if err != nil || !info.Mode().IsRegular() {
		return Result{}, validationFailure("invalid_configuration")
	}
	port, err := reserveLoopbackPort()
	if err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	username, password, err := ephemeralCredentials()
	if err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	encoded, err := buildVLESSClientConfig(target, port, username, password)
	if err != nil {
		return Result{}, err
	}
	directory := target.TempDirectory
	if directory == "" {
		directory = os.TempDir()
	}
	file, err := os.CreateTemp(directory, "aimili-vless-*.json")
	if err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	configPath := file.Name()
	defer file.Close()
	defer os.Remove(configPath)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return Result{}, validationFailure("protocol_failed")
	}
	if _, err := file.Write(encoded); err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	if err := file.Close(); err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	probeContext, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	command := exec.CommandContext(probeContext, target.XrayPath, "run", "-config", configPath)
	command.Dir = filepath.Dir(target.XrayPath)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		select {
		case <-waited:
		case <-time.After(time.Second):
		}
	}()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := waitForLoopback(probeContext, address, waited); err != nil {
		return Result{}, err
	}
	return v.ValidateSOCKS5H(probeContext, SOCKSTarget{
		Address: address, Username: username, Password: password,
		ProbeHost: target.ProbeHost, ExpectedExitIP: target.ExpectedExitIP,
	})
}

func buildVLESSClientConfig(target VLESSTarget, localPort int, username, password string) ([]byte, error) {
	host, rawPort, err := net.SplitHostPort(target.InboundAddress)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() ||
		target.ClientID == "" || target.PublicKey == "" || target.ShortID == "" ||
		target.ServerName == "" || localPort < 1 || localPort > 65535 || username == "" || password == "" {
		return nil, validationFailure("invalid_configuration")
	}
	serverPort, err := strconv.Atoi(rawPort)
	if err != nil || serverPort < 1 || serverPort > 65535 {
		return nil, validationFailure("invalid_configuration")
	}
	if len(target.MLDSA65Verify) > 4096 || strings.ContainsAny(target.MLDSA65Verify, "\x00\r\n") || target.MLDSA65Verify != strings.TrimSpace(target.MLDSA65Verify) {
		return nil, validationFailure("invalid_configuration")
	}
	realitySettings := map[string]any{"fingerprint": "chrome", "serverName": target.ServerName, "password": target.PublicKey, "shortId": target.ShortID, "spiderX": "/"}
	if target.MLDSA65Verify != "" {
		realitySettings["mldsa65Verify"] = target.MLDSA65Verify
	}
	document := map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"tag": "validation-socks", "listen": "127.0.0.1", "port": localPort, "protocol": "socks",
			"settings": map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": username, "pass": password}}, "udp": false},
		}},
		"outbounds": []any{map[string]any{
			"tag": "validation-vless", "protocol": "vless",
			"settings":       map[string]any{"vnext": []any{map[string]any{"address": host, "port": serverPort, "users": []any{map[string]any{"id": target.ClientID, "encryption": "none", "flow": "xtls-rprx-vision"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": realitySettings},
		}},
		"routing": map[string]any{"domainStrategy": "AsIs", "rules": []any{map[string]any{"type": "field", "inboundTag": []any{"validation-socks"}, "outboundTag": "validation-vless"}}},
	}
	return json.Marshal(document)
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func ephemeralCredentials() (string, string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(raw[:8]), hex.EncodeToString(raw[8:]), nil
}

func waitForLoopback(ctx context.Context, address string, processExit <-chan error) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return validationFailure("timeout")
			}
			return validationFailure("protocol_failed")
		case <-processExit:
			return validationFailure("protocol_failed")
		case <-ticker.C:
		}
	}
}
