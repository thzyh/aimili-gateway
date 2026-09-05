package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
)

type uiBundle struct {
	Manifest  []byte
	Signature []byte
	Archive   []byte
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: aimili-gateway-release-tool <keygen|ui> [options]")
		return 2
	}
	if args[0] == "keygen" {
		return runKeygen(args[1:], stdout, stderr)
	}
	if args[0] != "ui" {
		fmt.Fprintln(stderr, "usage: aimili-gateway-release-tool <keygen|ui> [options]")
		return 2
	}
	flags := flag.NewFlagSet("ui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dist := flags.String("dist", "", "Vite distribution directory")
	keyFile := flags.String("private-key", "", "Ed25519 private key file")
	output := flags.String("out", "", "release output directory")
	commit := flags.String("commit", "", "source commit")
	builtAt := flags.String("built-at", "", "RFC3339 build time")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *dist == "" || *keyFile == "" || *output == "" || *commit == "" || *builtAt == "" {
		fmt.Fprintln(stderr, "dist, private-key, out, commit and built-at are required")
		return 2
	}
	if pathWithin(*output, *keyFile) || pathWithin(*dist, *keyFile) {
		fmt.Fprintln(stderr, "private key must be outside the distribution and output directories")
		return 2
	}
	privateKey, err := readPrivateKey(*keyFile)
	if err != nil {
		fmt.Fprintln(stderr, "invalid private key")
		return 2
	}
	bundle, err := buildUIBundle(*dist, privateKey, *commit, *builtAt)
	if err != nil {
		fmt.Fprintln(stderr, "build failed")
		return 1
	}
	if err := os.MkdirAll(*output, 0o700); err != nil {
		fmt.Fprintln(stderr, "create output failed")
		return 1
	}
	for name, body := range map[string][]byte{"manifest.json": bundle.Manifest, "manifest.sig": bundle.Signature, "ui.tar.gz": bundle.Archive} {
		if err := writeNew(filepath.Join(*output, name), body); err != nil {
			fmt.Fprintln(stderr, "write output failed")
			return 1
		}
	}
	manifest, err := releaseverify.ParseUI(bundle.Manifest, "v1")
	if err != nil {
		fmt.Fprintln(stderr, "verify output failed")
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{"version": manifest.Version, "manifestBytes": len(bundle.Manifest), "archiveBytes": len(bundle.Archive)})
	return 0
}

func runKeygen(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	privatePath := flags.String("private", "", "new private key file")
	publicPath := flags.String("public", "", "new public key file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *privatePath == "" || *publicPath == "" || samePath(*privatePath, *publicPath) {
		fmt.Fprintln(stderr, "distinct private and public paths are required")
		return 2
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		fmt.Fprintln(stderr, "key generation failed")
		return 1
	}
	if err := writeNew(*privatePath, []byte(hex.EncodeToString(privateKey))); err != nil {
		fmt.Fprintln(stderr, "private key creation failed")
		return 1
	}
	if err := writeNew(*publicPath, []byte(hex.EncodeToString(publicKey))); err != nil {
		_ = os.Remove(*privatePath)
		fmt.Fprintln(stderr, "public key creation failed")
		return 1
	}
	fmt.Fprintln(stdout, "key_pair_created")
	return 0
}

func samePath(first, second string) bool {
	firstPath, firstErr := filepath.Abs(first)
	secondPath, secondErr := filepath.Abs(second)
	return firstErr == nil && secondErr == nil && strings.EqualFold(filepath.Clean(firstPath), filepath.Clean(secondPath))
}

func buildUIBundle(dist string, privateKey ed25519.PrivateKey, commit, builtAt string) (uiBundle, error) {
	if len(privateKey) != ed25519.PrivateKeySize || strings.TrimSpace(commit) == "" {
		return uiBundle{}, errors.New("invalid release metadata")
	}
	if _, err := time.Parse(time.RFC3339, builtAt); err != nil {
		return uiBundle{}, err
	}
	files, err := collectFiles(dist)
	if err != nil {
		return uiBundle{}, err
	}
	var archive bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&archive, gzip.BestCompression)
	if err != nil {
		return uiBundle{}, err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	manifestFiles := make([]releaseverify.File, 0, len(files))
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dist, filepath.FromSlash(name)))
		if err != nil {
			return uiBundle{}, err
		}
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), ModTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0, Format: tar.FormatUSTAR}
		if err := tarWriter.WriteHeader(header); err != nil {
			return uiBundle{}, err
		}
		if _, err := tarWriter.Write(body); err != nil {
			return uiBundle{}, err
		}
		digest := sha256.Sum256(body)
		manifestFiles = append(manifestFiles, releaseverify.File{Path: name, SHA256: fmt.Sprintf("%x", digest), Bytes: int64(len(body))})
	}
	if err := tarWriter.Close(); err != nil {
		return uiBundle{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return uiBundle{}, err
	}
	archiveDigest := sha256.Sum256(archive.Bytes())
	version := fmt.Sprintf("%x", archiveDigest)
	manifest, err := json.Marshal(releaseverify.UIManifest{
		SchemaVersion: 1, Kind: "ui", Version: version, Commit: commit, BuiltAt: builtAt, APIVersion: "v1",
		Archive: releaseverify.Payload{SHA256: version, Bytes: int64(archive.Len())}, Files: manifestFiles,
	})
	if err != nil {
		return uiBundle{}, err
	}
	return uiBundle{Manifest: manifest, Signature: ed25519.Sign(privateKey, manifest), Archive: archive.Bytes()}, nil
}

func collectFiles(dist string) ([]string, error) {
	root, err := filepath.Abs(dist)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil || relative == "." {
			return err
		}
		name := filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("distribution contains a link")
		}
		if entry.IsDir() {
			if name != "assets" && !strings.HasPrefix(name, "assets/") {
				return errors.New("distribution contains an unexpected directory")
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || (name != "index.html" && !strings.HasPrefix(name, "assets/")) {
			return errors.New("distribution contains an unexpected file")
		}
		files = append(files, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if index := sort.SearchStrings(files, "index.html"); index >= len(files) || files[index] != "index.html" {
		return nil, errors.New("distribution index is missing")
	}
	return files, nil
}

func readPrivateKey(filename string) (ed25519.PrivateKey, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("private key file is unsafe")
	}
	body, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("private key is invalid")
	}
	return ed25519.PrivateKey(key), nil
}

func pathWithin(root, candidate string) bool {
	rootPath, rootErr := filepath.Abs(root)
	candidatePath, candidateErr := filepath.Abs(candidate)
	if rootErr != nil || candidateErr != nil {
		return false
	}
	relative, err := filepath.Rel(rootPath, candidatePath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func writeNew(filename string, body []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
