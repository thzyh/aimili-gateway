package validator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
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
	return v.ValidatePublic(ctx, PublicTarget{
		Mode:     domain.ProtocolVLESSTCPRealityVision,
		XrayPath: target.XrayPath, TempDirectory: target.TempDirectory, InboundAddress: target.InboundAddress,
		ClientID: target.ClientID, PublicKey: target.PublicKey, ShortID: target.ShortID, ServerName: target.ServerName,
		ProbeHost: target.ProbeHost, ExpectedExitIP: target.ExpectedExitIP, MLDSA65Verify: target.MLDSA65Verify,
	})
}

func buildVLESSClientConfig(target VLESSTarget, localPort int, username, password string) ([]byte, error) {
	return buildPublicClientConfig(PublicTarget{
		Mode: domain.ProtocolVLESSTCPRealityVision, InboundAddress: target.InboundAddress,
		ClientID: target.ClientID, PublicKey: target.PublicKey, ShortID: target.ShortID,
		ServerName: target.ServerName, MLDSA65Verify: target.MLDSA65Verify,
	}, localPort, username, password)
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
