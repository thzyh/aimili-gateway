package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thzyh/aimili-gateway/internal/auth"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/store"
)

const enrollmentIssuer = "Aimili Gateway"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now))
}

func run(args []string, in io.Reader, out, errOut io.Writer, now func() time.Time) int {
	if len(args) != 1 || (args[0] != "init" && args[0] != "revoke-sessions") {
		_, _ = fmt.Fprintln(errOut, "usage: aimili-gateway-admin <init|revoke-sessions>")
		return 2
	}
	cfg, err := config.Load(os.Getenv("GATEWAY_CONFIG"))
	if err != nil {
		writeCommandError(errOut, "load configuration", err)
		return 1
	}
	database, err := store.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		writeCommandError(errOut, "open gateway database", err)
		return 1
	}
	defer database.Close()

	switch args[0] {
	case "init":
		if err := initializeAdmin(context.Background(), database, cfg.MasterKeyFile, in, out, now); err != nil {
			writeCommandError(errOut, "initialize administrator", err)
			return 1
		}
	case "revoke-sessions":
		if err := database.RevokeAllSessions(context.Background()); err != nil {
			writeCommandError(errOut, "revoke sessions", err)
			return 1
		}
		_, _ = fmt.Fprintln(out, "All gateway sessions have been revoked.")
	}
	return 0
}

func initializeAdmin(ctx context.Context, database *store.Store, masterKeyPath string, in io.Reader, out io.Writer, now func() time.Time) error {
	if _, err := database.GetAdmin(ctx); err == nil {
		return store.ErrAdminExists
	} else if !errors.Is(err, store.ErrAdminNotFound) {
		return err
	}
	prompts := newPromptReader(in)
	username, err := prompts.prompt(out, "Username: ", false)
	if err != nil {
		return errors.New("read username")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username is required")
	}
	passwordText, err := prompts.prompt(out, "Password: ", true)
	if err != nil {
		return errors.New("read password")
	}
	confirmationText, err := prompts.prompt(out, "Confirm password: ", true)
	if err != nil {
		return errors.New("read password confirmation")
	}
	password := []byte(passwordText)
	confirmation := []byte(confirmationText)
	defer clear(password)
	defer clear(confirmation)
	if utf8.RuneCount(password) < 12 {
		return errors.New("password must contain at least 12 characters")
	}
	if strings.TrimSpace(passwordText) != passwordText {
		return errors.New("password must not have leading or trailing whitespace")
	}
	if !equalBytes(password, confirmation) {
		return errors.New("password confirmation does not match")
	}

	masterKey, err := loadOrCreateMasterKey(masterKeyPath)
	if err != nil {
		return err
	}
	defer clear(masterKey)
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	totpSecret, err := auth.GenerateTOTPSecret()
	if err != nil {
		return errors.New("generate TOTP secret")
	}
	defer clear(totpSecret)
	encryptedSecret, err := auth.Seal(masterKey, totpSecret)
	if err != nil {
		return err
	}
	timestamp := now().UTC()
	if err := database.CreateAdmin(ctx, store.Admin{
		Username:             username,
		PasswordHash:         []byte(passwordHash),
		TOTPSecretCiphertext: encryptedSecret,
		CreatedAt:            timestamp,
		SecurityUpdatedAt:    timestamp,
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, buildEnrollmentURI(username, totpSecret)); err != nil {
		return errors.New("write enrollment URI")
	}
	_, _ = fmt.Fprintln(out, "Store the second factor safely. Losing it requires local recovery.")
	return nil
}

func buildEnrollmentURI(username string, secret []byte) string {
	label := url.PathEscape(enrollmentIssuer) + ":" + url.PathEscape(username)
	query := make(url.Values)
	query.Set("secret", base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret))
	query.Set("issuer", enrollmentIssuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", "6")
	query.Set("period", "30")
	return "otpauth://totp/" + label + "?" + query.Encode()
}

func loadOrCreateMasterKey(path string) ([]byte, error) {
	key, err := loadMasterKey(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("create master key directory")
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, errors.New("generate master key")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		clear(key)
		return loadMasterKey(path)
	}
	if err != nil {
		clear(key)
		return nil, errors.New("create master key")
	}
	removeIncomplete := true
	defer func() {
		_ = file.Close()
		if removeIncomplete {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(key); err != nil {
		clear(key)
		return nil, errors.New("write master key")
	}
	if err := file.Sync(); err != nil {
		clear(key)
		return nil, errors.New("sync master key")
	}
	if err := file.Close(); err != nil {
		clear(key)
		return nil, errors.New("close master key")
	}
	removeIncomplete = false
	return key, nil
}

func loadMasterKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("master key must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("master key permissions must not allow group or world access")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read master key")
	}
	if len(key) != 32 {
		clear(key)
		return nil, errors.New("master key must be exactly 32 bytes")
	}
	return key, nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	difference := byte(0)
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func writeCommandError(destination io.Writer, operation string, err error) {
	_, _ = fmt.Fprintf(destination, "%s: %v\n", operation, err)
}

type promptReader struct {
	reader *bufio.Reader
	file   *os.File
}

func newPromptReader(input io.Reader) *promptReader {
	prompt := &promptReader{reader: bufio.NewReader(input)}
	if file, ok := input.(*os.File); ok {
		prompt.file = file
	}
	return prompt
}

func (p *promptReader) prompt(out io.Writer, label string, secret bool) (string, error) {
	if _, err := fmt.Fprint(out, label); err != nil {
		return "", err
	}
	hidden := secret && p.file != nil && isTerminal(p.file)
	var restore func() error
	if hidden {
		var err error
		restore, err = disableTerminalEcho(p.file)
		if err != nil {
			return "", err
		}
	}
	line, readErr := p.reader.ReadString('\n')
	if restore != nil {
		restoreErr := restore()
		_, _ = fmt.Fprintln(out)
		if readErr == nil && restoreErr != nil {
			readErr = restoreErr
		}
	}
	if readErr != nil && !(errors.Is(readErr, io.EOF) && len(line) > 0) {
		return "", readErr
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
