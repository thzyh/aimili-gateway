package updatetxn

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func RemoveRequest(dir, runID string) error {
	if !hexIdentifier.MatchString(runID) {
		return ErrInvalidRequest
	}
	err := os.Remove(filepath.Join(dir, runID+".json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(dir)
}

func PruneResults(resultDir, requestDir, activeRun string, limit int) error {
	if limit < 2 {
		return ErrInvalidRequest
	}
	var lease leaseRecord
	err := readTrustedJSON(filepath.Join(requestDir, ".update.lease"), nil, &lease)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(resultDir)
	if err != nil {
		return err
	}
	type item struct {
		name string
		time int64
	}
	var files []item
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !strings.HasSuffix(entry.Name(), ".json") || !hexIdentifier.MatchString(id) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return ErrUntrustedResult
		}
		files = append(files, item{entry.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].time < files[j].time })
	remaining := len(files)
	for _, file := range files {
		if remaining <= limit {
			break
		}
		id := strings.TrimSuffix(file.name, ".json")
		if id == activeRun || id == lease.RunID {
			continue
		}
		if err := os.Remove(filepath.Join(resultDir, file.name)); err != nil {
			return err
		}
		remaining--
	}
	return syncDirectory(resultDir)
}
