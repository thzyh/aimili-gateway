package validator

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestValidateSOCKS5HUsesAuthenticationAndRemoteDomainRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	domainSeen := make(chan string, 1)
	go serveOneSOCKSProbe(listener, domainSeen)

	validator := New(3 * time.Second)
	result, err := validator.ValidateSOCKS5H(context.Background(), SOCKSTarget{
		Address: listener.Addr().String(), Username: "proxy-user", Password: "proxy-password",
		ProbeHost: "check.example", ExpectedExitIP: "203.0.113.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DNSVerified || result.ExitIP != "203.0.113.9" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if domain := <-domainSeen; domain != "check.example" {
		t.Fatalf("SOCKS request used domain %q", domain)
	}
}

func TestValidateSOCKS5HRejectsExitMismatch(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go serveOneSOCKSProbe(listener, make(chan string, 1))
	validator := New(3 * time.Second)
	_, err = validator.ValidateSOCKS5H(context.Background(), SOCKSTarget{
		Address: listener.Addr().String(), Username: "proxy-user", Password: "proxy-password",
		ProbeHost: "check.example", ExpectedExitIP: "203.0.113.10",
	})
	if codeOf(err) != "egress_mismatch" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func serveOneSOCKSProbe(listener net.Listener, domainSeen chan<- string) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	header := make([]byte, 3)
	_, _ = io.ReadFull(reader, header)
	_, _ = connection.Write([]byte{5, 2})
	authHeader := make([]byte, 2)
	_, _ = io.ReadFull(reader, authHeader)
	username := make([]byte, int(authHeader[1]))
	_, _ = io.ReadFull(reader, username)
	passwordLength, _ := reader.ReadByte()
	password := make([]byte, int(passwordLength))
	_, _ = io.ReadFull(reader, password)
	if string(username) != "proxy-user" || string(password) != "proxy-password" {
		_, _ = connection.Write([]byte{1, 1})
		return
	}
	_, _ = connection.Write([]byte{1, 0})
	requestHeader := make([]byte, 5)
	_, _ = io.ReadFull(reader, requestHeader)
	domain := make([]byte, int(requestHeader[4]))
	_, _ = io.ReadFull(reader, domain)
	port := make([]byte, 2)
	_, _ = io.ReadFull(reader, port)
	if binary.BigEndian.Uint16(port) != 80 {
		return
	}
	domainSeen <- string(domain)
	_, _ = connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
	for {
		line, _ := reader.ReadString('\n')
		if line == "\r\n" || line == "" {
			break
		}
	}
	_, _ = connection.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\n203.0.113.9"))
}

func codeOf(err error) string {
	if validationError, ok := err.(*Error); ok {
		return validationError.Code
	}
	return ""
}
