package validator

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type SOCKSTarget struct {
	Address        string
	Username       string
	Password       string
	ProbeHost      string
	ExpectedExitIP string
}

func (v *Validator) ValidateSOCKS5H(ctx context.Context, target SOCKSTarget) (Result, error) {
	if v == nil {
		return Result{}, validationFailure("invalid_configuration")
	}
	host, _, err := net.SplitHostPort(target.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() ||
		target.Username == "" || target.Password == "" || len(target.Username) > 255 || len(target.Password) > 255 ||
		target.ProbeHost == "" || len(target.ProbeHost) > 255 || net.ParseIP(target.ProbeHost) != nil || net.ParseIP(target.ExpectedExitIP) == nil {
		return Result{}, validationFailure("invalid_configuration")
	}
	probeContext, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	started := time.Now()
	connection, err := (&net.Dialer{}).DialContext(probeContext, "tcp", target.Address)
	if err != nil {
		return Result{}, mapNetworkError(err)
	}
	defer connection.Close()
	if deadline, ok := probeContext.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	reader := bufio.NewReader(connection)
	if _, err := connection.Write([]byte{5, 1, 2}); err != nil {
		return Result{}, mapNetworkError(err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(reader, response); err != nil || response[0] != 5 || response[1] != 2 {
		return Result{}, validationFailure("authentication_failed")
	}
	auth := []byte{1, byte(len(target.Username))}
	auth = append(auth, target.Username...)
	auth = append(auth, byte(len(target.Password)))
	auth = append(auth, target.Password...)
	if _, err := connection.Write(auth); err != nil {
		return Result{}, mapNetworkError(err)
	}
	if _, err := io.ReadFull(reader, response); err != nil || response[0] != 1 || response[1] != 0 {
		return Result{}, validationFailure("authentication_failed")
	}
	request := []byte{5, 1, 0, 3, byte(len(target.ProbeHost))}
	request = append(request, target.ProbeHost...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, 80)
	request = append(request, port...)
	if _, err := connection.Write(request); err != nil {
		return Result{}, mapNetworkError(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(reader, reply); err != nil || reply[0] != 5 || reply[1] != 0 {
		return Result{}, validationFailure("dns_failed")
	}
	if err := discardSOCKSAddress(reader, reply[3]); err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	httpRequest, _ := http.NewRequest(http.MethodGet, "http://"+target.ProbeHost+"/", nil)
	httpRequest.Header.Set("Connection", "close")
	if err := httpRequest.Write(connection); err != nil {
		return Result{}, mapNetworkError(err)
	}
	httpResponse, err := http.ReadResponse(reader, httpRequest)
	if err != nil || httpResponse.StatusCode != http.StatusOK {
		return Result{}, validationFailure("protocol_failed")
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, 128))
	if err != nil {
		return Result{}, validationFailure("protocol_failed")
	}
	exitIP := strings.TrimSpace(string(body))
	if net.ParseIP(exitIP) == nil {
		return Result{}, validationFailure("protocol_failed")
	}
	if exitIP != target.ExpectedExitIP {
		return Result{}, validationFailure("egress_mismatch")
	}
	return Result{ExitIP: exitIP, DNSVerified: true, Latency: time.Since(started)}, nil
}

func discardSOCKSAddress(reader io.Reader, addressType byte) error {
	length := 0
	switch addressType {
	case 1:
		length = 4
	case 4:
		length = 16
	case 3:
		value := make([]byte, 1)
		if _, err := io.ReadFull(reader, value); err != nil {
			return err
		}
		length = int(value[0])
	default:
		return io.ErrUnexpectedEOF
	}
	_, err := io.CopyN(io.Discard, reader, int64(length+2))
	return err
}

func mapNetworkError(err error) error {
	if networkError, ok := err.(net.Error); ok && networkError.Timeout() {
		return validationFailure("timeout")
	}
	return validationFailure("protocol_failed")
}
