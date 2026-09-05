package uirelease

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
)

const minimumUIReserve = 64 << 20

type PointerStore interface {
	Get(root, name string) (string, error)
	Set(root, name, version string) error
	Remove(root, name string) error
}

type Config struct {
	StagingDir     string
	Root           string
	PublicKeyFile  string
	APIVersion     string
	AvailableBytes func(string) (uint64, error)
	Pointers       PointerStore
	HealthCheck    func(context.Context, string) error
}

type Result struct {
	Version         string
	PreviousVersion string
	State           string
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.code + ": " + e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return releaseverify.ErrorCode(err)
}

func Install(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Pointers == nil {
		cfg.Pointers = osPointers{}
	}
	if err := validateStaging(cfg.StagingDir); err != nil {
		return Result{}, &codedError{code: "invalid_staging", err: err}
	}
	manifestBody, err := readRegular(filepath.Join(cfg.StagingDir, "manifest.json"), 1<<20)
	if err != nil {
		return Result{}, &codedError{code: "invalid_staging", err: err}
	}
	signature, err := readRegular(filepath.Join(cfg.StagingDir, "manifest.sig"), ed25519.SignatureSize)
	if err != nil {
		return Result{}, &codedError{code: "invalid_staging", err: err}
	}
	archive, err := readRegular(filepath.Join(cfg.StagingDir, "ui.tar.gz"), 256<<20)
	if err != nil {
		return Result{}, &codedError{code: "invalid_staging", err: err}
	}
	publicKey, err := readPublicKey(cfg.PublicKeyFile)
	if err != nil {
		return Result{}, &codedError{code: "invalid_public_key", err: err}
	}
	manifest, err := releaseverify.VerifyUI(manifestBody, signature, archive, publicKey, cfg.APIVersion)
	if err != nil {
		if releaseverify.ErrorCode(err) == "invalid_manifest" {
			return Result{}, &codedError{code: "invalid_archive", err: err}
		}
		return Result{}, err
	}
	if cfg.AvailableBytes == nil {
		return Result{}, &codedError{code: "disk_check_failed", err: errors.New("available space provider is required")}
	}
	available, err := cfg.AvailableBytes(cfg.Root)
	if err != nil {
		return Result{}, &codedError{code: "disk_check_failed", err: err}
	}
	required := uint64(len(archive))*3 + minimumUIReserve
	if available < required {
		return Result{}, &codedError{code: "disk_full", err: errors.New("insufficient space for UI release")}
	}
	releasesRoot := filepath.Join(cfg.Root, "releases")
	if err := os.MkdirAll(releasesRoot, 0o755); err != nil {
		return Result{}, &codedError{code: "install_failed", err: err}
	}
	temporary, err := os.MkdirTemp(releasesRoot, ".install-")
	if err != nil {
		return Result{}, &codedError{code: "install_failed", err: err}
	}
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := extractArchive(archive, temporary, manifest.Files); err != nil {
		return Result{}, &codedError{code: "invalid_archive", err: err}
	}
	if err := os.WriteFile(filepath.Join(temporary, "manifest.json"), manifestBody, 0o644); err != nil {
		return Result{}, &codedError{code: "install_failed", err: err}
	}
	finalPath := filepath.Join(releasesRoot, manifest.Version)
	if err := os.Rename(temporary, finalPath); err != nil {
		return Result{}, &codedError{code: "install_failed", err: err}
	}
	keepTemporary = true
	oldCurrent, currentErr := cfg.Pointers.Get(cfg.Root, "current")
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		_ = os.RemoveAll(finalPath)
		return Result{}, &codedError{code: "pointer_failed", err: currentErr}
	}
	oldPrevious, previousErr := cfg.Pointers.Get(cfg.Root, "previous")
	if previousErr != nil && !errors.Is(previousErr, os.ErrNotExist) {
		_ = os.RemoveAll(finalPath)
		return Result{}, &codedError{code: "pointer_failed", err: previousErr}
	}
	if oldCurrent != "" {
		if err := cfg.Pointers.Set(cfg.Root, "previous", oldCurrent); err != nil {
			_ = os.RemoveAll(finalPath)
			return Result{}, &codedError{code: "pointer_failed", err: err}
		}
	}
	if err := cfg.Pointers.Set(cfg.Root, "current", manifest.Version); err != nil {
		restorePointer(cfg.Pointers, cfg.Root, "previous", oldPrevious)
		_ = os.RemoveAll(finalPath)
		return Result{}, &codedError{code: "pointer_failed", err: err}
	}
	if cfg.HealthCheck != nil {
		if err := cfg.HealthCheck(ctx, manifest.Version); err != nil {
			restorePointer(cfg.Pointers, cfg.Root, "current", oldCurrent)
			restorePointer(cfg.Pointers, cfg.Root, "previous", oldPrevious)
			_ = os.RemoveAll(finalPath)
			return Result{}, &codedError{code: "health_check_failed", err: err}
		}
	}
	if err := removeOlderReleases(releasesRoot, manifest.Version, oldCurrent); err != nil {
		return Result{}, &codedError{code: "cleanup_failed", err: err}
	}
	return Result{Version: manifest.Version, PreviousVersion: oldCurrent, State: "success"}, nil
}

func validateStaging(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	wanted := []string{"manifest.json", "manifest.sig", "ui.tar.gz"}
	if len(entries) != len(wanted) {
		return errors.New("staging must contain exactly three release assets")
	}
	for index, entry := range entries {
		if entry.Name() != wanted[index] || !entry.Type().IsRegular() {
			return errors.New("staging contains an unexpected asset")
		}
	}
	return nil
}

func Rollback(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Pointers == nil {
		cfg.Pointers = osPointers{}
	}
	current, err := cfg.Pointers.Get(cfg.Root, "current")
	if err != nil {
		return Result{}, &codedError{code: "rollback_unavailable", err: err}
	}
	previous, err := cfg.Pointers.Get(cfg.Root, "previous")
	if err != nil {
		return Result{}, &codedError{code: "rollback_unavailable", err: err}
	}
	for _, version := range []string{current, previous} {
		info, statErr := os.Stat(filepath.Join(cfg.Root, "releases", version))
		if statErr != nil || !info.IsDir() {
			return Result{}, &codedError{code: "rollback_unavailable", err: errors.New("release directory is unavailable")}
		}
	}
	if err := cfg.Pointers.Set(cfg.Root, "current", previous); err != nil {
		return Result{}, &codedError{code: "pointer_failed", err: err}
	}
	if err := cfg.Pointers.Set(cfg.Root, "previous", current); err != nil {
		restorePointer(cfg.Pointers, cfg.Root, "current", current)
		return Result{}, &codedError{code: "pointer_failed", err: err}
	}
	if cfg.HealthCheck != nil {
		if err := cfg.HealthCheck(ctx, previous); err != nil {
			restorePointer(cfg.Pointers, cfg.Root, "current", current)
			restorePointer(cfg.Pointers, cfg.Root, "previous", previous)
			return Result{}, &codedError{code: "health_check_failed", err: err}
		}
	}
	if err := removeOlderReleases(filepath.Join(cfg.Root, "releases"), previous, current); err != nil {
		return Result{}, &codedError{code: "cleanup_failed", err: err}
	}
	return Result{Version: previous, PreviousVersion: current, State: "success"}, nil
}

func readRegular(filename string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maximum {
		return nil, errors.New("release asset is not a bounded regular file")
	}
	return os.ReadFile(filename)
}

func readPublicKey(filename string) (ed25519.PublicKey, error) {
	body, err := readRegular(filename, 256)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key is invalid")
	}
	return ed25519.PublicKey(raw), nil
}

func extractArchive(archive []byte, destination string, files []releaseverify.File) error {
	expected := make(map[string]releaseverify.File, len(files))
	for _, file := range files {
		expected[file.Path] = file
	}
	gzipReader, err := gzip.NewReader(bytesReader(archive))
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	seen := make(map[string]bool, len(files))
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || path.Clean(header.Name) != header.Name || strings.Contains(header.Name, `\`) || strings.HasPrefix(header.Name, "/") {
			return errors.New("archive contains an unsafe entry")
		}
		file, ok := expected[header.Name]
		if !ok || seen[header.Name] || header.Size != file.Bytes || header.Mode&0o777 != 0o644 {
			return errors.New("archive entry does not match manifest")
		}
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		relative, err := filepath.Rel(destination, target)
		if err != nil || strings.HasPrefix(relative, "..") {
			return errors.New("archive entry escapes destination")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(tarReader, header.Size+1))
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size || fmt.Sprintf("%x", hash.Sum(nil)) != file.SHA256 {
			return errors.New("archive file content does not match manifest")
		}
		seen[header.Name] = true
	}
	if len(seen) != len(expected) {
		return errors.New("archive is incomplete")
	}
	return nil
}

func bytesReader(value []byte) *strings.Reader { return strings.NewReader(string(value)) }

func restorePointer(pointers PointerStore, root, name, version string) {
	if version == "" {
		_ = pointers.Remove(root, name)
		return
	}
	_ = pointers.Set(root, name, version)
}

func removeOlderReleases(releasesRoot string, keep ...string) error {
	allowed := make(map[string]bool, len(keep))
	for _, version := range keep {
		if version != "" {
			allowed[version] = true
		}
	}
	entries, err := os.ReadDir(releasesRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validVersion(entry.Name()) || allowed[entry.Name()] {
			continue
		}
		target := filepath.Join(releasesRoot, entry.Name())
		relative, err := filepath.Rel(releasesRoot, target)
		if err != nil || relative != entry.Name() {
			return errors.New("release cleanup escaped root")
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	return nil
}

func validVersion(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == sha256.Size*2 && value == strings.ToLower(value) && err == nil
}

type osPointers struct{}

func (osPointers) Get(root, name string) (string, error) {
	target, err := os.Readlink(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	clean := filepath.ToSlash(filepath.Clean(target))
	parts := strings.Split(clean, "/")
	if len(parts) != 2 || parts[0] != "releases" || !validVersion(parts[1]) {
		return "", errors.New("release pointer is invalid")
	}
	return parts[1], nil
}

func (osPointers) Set(root, name, version string) error {
	if !validVersion(version) || (name != "current" && name != "previous") {
		return errors.New("release pointer input is invalid")
	}
	temporary := filepath.Join(root, "."+name+"-new")
	_ = os.Remove(temporary)
	if err := os.Symlink(filepath.Join("releases", version), temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(root, name)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (osPointers) Remove(root, name string) error {
	err := os.Remove(filepath.Join(root, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
