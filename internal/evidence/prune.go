package evidence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type PrunedTelemetry struct {
	Time   time.Time `json:"time"`
	Run    string    `json:"run"`
	File   string    `json:"file"`
	Bytes  int64     `json:"bytes"`
	Reason string    `json:"reason"`
}

type telemetryFile struct {
	run      string
	path     string
	size     int64
	modified time.Time
}

func (r *Root) PruneTelemetry(budget int64, active map[string]bool) ([]PrunedTelemetry, error) {
	files, total, err := r.telemetryFiles()
	if err != nil {
		return nil, err
	}
	var pruned []PrunedTelemetry
	for _, f := range files {
		if total <= budget {
			break
		}
		if active[f.run] {
			continue
		}
		record := PrunedTelemetry{Time: time.Now().UTC(), Run: f.run, File: TelemetryFile, Bytes: f.size, Reason: fmt.Sprintf("telemetry total %d bytes exceeds budget %d", total, budget)}
		if err := appendDurable(r.PruningPath(), record); err != nil {
			return pruned, fmt.Errorf("record pruning before deletion: %w", err)
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return pruned, fmt.Errorf("prune %s: %w", f.path, err)
		}
		total -= f.size
		pruned = append(pruned, record)
	}
	return pruned, nil
}

func (r *Root) telemetryFiles() ([]telemetryFile, int64, error) {
	entries, err := os.ReadDir(filepath.Join(r.path, RunsDir))
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	var files []telemetryFile
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() || !runNamePattern.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(r.path, RunsDir, entry.Name(), TelemetryFile)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		files = append(files, telemetryFile{run: entry.Name(), path: path, size: info.Size(), modified: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified.Equal(files[j].modified) {
			return files[i].run < files[j].run
		}
		return files[i].modified.Before(files[j].modified)
	})
	return files, total, nil
}
