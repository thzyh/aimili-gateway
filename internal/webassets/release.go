package webassets

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
)

var entryAssetPattern = regexp.MustCompile(`(?:src|href)=["'](/assets/[^"'?#]+)`)

func openCurrent(root, apiVersion string) (fs.FS, string, error) {
	return openCurrentWithResolver(root, apiVersion, nil)
}

func openCurrentWithResolver(root, apiVersion string, resolve func(string) (string, error)) (fs.FS, string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, "", errors.New("external UI is not configured")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return nil, "", errors.New("resolve external UI root")
	}
	if resolve == nil {
		resolve = filepath.EvalSymlinks
	}
	target, err := resolve(filepath.Join(rootPath, "current"))
	if err != nil {
		return nil, "", errors.New("resolve current UI release")
	}
	relative, err := filepath.Rel(rootPath, target)
	if err != nil {
		return nil, "", errors.New("resolve current UI boundary")
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 2 || parts[0] != "releases" || !validVersionID(parts[1]) {
		return nil, "", errors.New("current UI release escapes configured root")
	}
	releasePath := filepath.Join(rootPath, "releases", parts[1])
	info, err := os.Stat(releasePath)
	if err != nil || !info.IsDir() {
		return nil, "", errors.New("current UI release is unavailable")
	}
	manifestBody, err := os.ReadFile(filepath.Join(releasePath, "manifest.json"))
	if err != nil {
		return nil, "", errors.New("read current UI manifest")
	}
	manifest, err := releaseverify.ParseUI(manifestBody, apiVersion)
	if err != nil || manifest.Version != parts[1] {
		return nil, "", errors.New("current UI manifest is incompatible")
	}
	if err := validateReleaseFiles(releasePath, manifest); err != nil {
		return nil, "", err
	}
	if info, err := os.Lstat(filepath.Join(releasePath, "index.html")); err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("current UI index is unavailable")
	}
	index, err := os.ReadFile(filepath.Join(releasePath, "index.html"))
	if err != nil {
		return nil, "", errors.New("read current UI index")
	}
	for _, match := range entryAssetPattern.FindAllSubmatch(index, -1) {
		assetPath := filepath.Join(releasePath, filepath.FromSlash(strings.TrimPrefix(string(match[1]), "/")))
		assetInfo, statErr := os.Lstat(assetPath)
		if statErr != nil || !assetInfo.Mode().IsRegular() {
			return nil, "", errors.New("current UI entry asset is unavailable")
		}
	}
	return os.DirFS(releasePath), manifest.Version, nil
}

func validVersionID(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateReleaseFiles(releasePath string, manifest releaseverify.UIManifest) error {
	expected := make(map[string]int64, len(manifest.Files))
	previous := ""
	for _, file := range manifest.Files {
		clean := pathpkg.Clean(file.Path)
		if clean != file.Path || strings.Contains(file.Path, `\`) || (file.Path != "index.html" && !strings.HasPrefix(file.Path, "assets/")) || file.Bytes < 0 || !validVersionID(file.SHA256) || file.Path <= previous {
			return errors.New("current UI manifest file list is invalid")
		}
		previous = file.Path
		expected[file.Path] = file.Bytes
	}
	if _, ok := expected["index.html"]; !ok {
		return errors.New("current UI manifest is missing index")
	}
	seen := make(map[string]bool, len(expected))
	err := filepath.WalkDir(releasePath, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(releasePath, current)
		if err != nil || relative == "." {
			return err
		}
		name := filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("current UI release contains a link")
		}
		if entry.IsDir() {
			if name != "assets" && !strings.HasPrefix(name, "assets/") {
				return errors.New("current UI release contains an unexpected directory")
			}
			return nil
		}
		if name == "manifest.json" {
			return nil
		}
		want, ok := expected[name]
		if !ok {
			return errors.New("current UI release contains an unlisted file")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() != want {
			return errors.New("current UI release file metadata is invalid")
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("current UI release is incomplete")
	}
	return nil
}
