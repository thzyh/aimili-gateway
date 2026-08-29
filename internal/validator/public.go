package validator

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

type PublicTarget struct {
	Mode           domain.ProtocolMode
	XrayPath       string
	TempDirectory  string
	InboundAddress string
	ClientID       string
	Auth           string
	PublicKey      string
	ShortID        string
	ServerName     string
	MLDSA65Verify  string
	XHTTPPath      string
	TLSServerName  string
	ProbeHost      string
	ExpectedExitIP string
}

func (v *Validator) ValidatePublic(ctx context.Context, target PublicTarget) (Result, error) {
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
	encoded, err := buildPublicClientConfig(target, port, username, password)
	if err != nil {
		return Result{}, err
	}
	directory := target.TempDirectory
	if directory == "" {
		directory = os.TempDir()
	}
	file, err := os.CreateTemp(directory, "aimili-public-*.json")
	if err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	configPath := file.Name()
	defer file.Close()
	defer os.Remove(configPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
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

func buildPublicClientConfig(target PublicTarget, localPort int, username, password string) ([]byte, error) {
	host, rawPort, err := net.SplitHostPort(target.InboundAddress)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || localPort < 1 || localPort > 65535 || username == "" || password == "" || !target.Mode.Valid() {
		return nil, validationFailure("invalid_configuration")
	}
	serverPort, err := strconv.Atoi(rawPort)
	if err != nil || serverPort < 1 || serverPort > 65535 {
		return nil, validationFailure("invalid_configuration")
	}
	outbound := map[string]any{"tag": "validation-public"}
	switch target.Mode {
	case domain.ProtocolVLESSTCPRealityVision, domain.ProtocolVLESSXHTTPReality:
		if !safePublicMaterial(target.ClientID, 128) || !safePublicMaterial(target.PublicKey, 512) || !safePublicMaterial(target.ShortID, 64) || !safePublicHost(target.ServerName) ||
			len(target.MLDSA65Verify) > 4096 || strings.ContainsAny(target.MLDSA65Verify, "\x00\r\n") || target.MLDSA65Verify != strings.TrimSpace(target.MLDSA65Verify) {
			return nil, validationFailure("invalid_configuration")
		}
		flow := "xtls-rprx-vision"
		stream := map[string]any{"network": "tcp", "security": "reality"}
		if target.Mode == domain.ProtocolVLESSXHTTPReality {
			if len(target.XHTTPPath) > 2048 || !strings.HasPrefix(target.XHTTPPath, "/") || strings.ContainsAny(target.XHTTPPath, "?#\\\x00\r\n") {
				return nil, validationFailure("invalid_configuration")
			}
			flow = ""
			stream["network"] = "xhttp"
			stream["xhttpSettings"] = map[string]any{"path": target.XHTTPPath, "mode": "auto"}
		} else if target.XHTTPPath != "" {
			return nil, validationFailure("invalid_configuration")
		}
		reality := map[string]any{"fingerprint": "chrome", "serverName": target.ServerName, "password": target.PublicKey, "shortId": target.ShortID, "spiderX": "/"}
		if target.MLDSA65Verify != "" {
			reality["mldsa65Verify"] = target.MLDSA65Verify
		}
		stream["realitySettings"] = reality
		outbound["protocol"] = "vless"
		outbound["settings"] = map[string]any{"vnext": []any{map[string]any{"address": host, "port": serverPort, "users": []any{map[string]any{"id": target.ClientID, "encryption": "none", "flow": flow}}}}}
		outbound["streamSettings"] = stream
	case domain.ProtocolHysteria2QUICTLS:
		if !safePublicMaterial(target.Auth, 512) || !safePublicHost(target.TLSServerName) || target.ClientID != "" || target.XHTTPPath != "" {
			return nil, validationFailure("invalid_configuration")
		}
		outbound["protocol"] = "hysteria"
		outbound["settings"] = map[string]any{"version": 2, "servers": []any{map[string]any{"address": host, "port": serverPort, "auth": target.Auth}}}
		outbound["streamSettings"] = map[string]any{
			"network": "hysteria", "security": "tls", "hysteriaSettings": map[string]any{"version": 2},
			"tlsSettings": map[string]any{"serverName": target.TLSServerName, "allowInsecure": false, "fingerprint": "chrome"},
		}
	}
	document := map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"tag": "validation-socks", "listen": "127.0.0.1", "port": localPort, "protocol": "socks",
			"settings": map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": username, "pass": password}}, "udp": false},
		}},
		"outbounds": []any{outbound},
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": []any{map[string]any{"type": "field", "inboundTag": []any{"validation-socks"}, "outboundTag": "validation-public"}}},
	}
	return json.Marshal(document)
}

func safePublicMaterial(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\x00\r\n\t ")
}

func safePublicHost(value string) bool {
	return value != "" && len(value) <= 253 && net.ParseIP(value) == nil && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "/:\\?#\x00\r\n\t ")
}
