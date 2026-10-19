package evidence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

var ErrTelemetryBudgetExceeded = errors.New("telemetry budget exceeded by active runs")

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
			return pruned, nil
		}
		if active[f.run] {
			continue
		}
		record, err := r.pruneFile(f, total, budget)
		if err != nil {
			return pruned, err
		}
		total -= record.Bytes
		pruned = append(pruned, record)
	}
	if total > budget {
		return pruned, fmt.Errorf("%w: %d bytes remain against a %d byte budget", ErrTelemetryBudgetExceeded, total, budget)
	}
	return pruned, nil
}

func (r *Root) pruneFile(f telemetryFile, total, budget int64) (PrunedTelemetry, error) {
	file, err := os.OpenFile(f.path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return PrunedTelemetry{}, fmt.Errorf("open %s for pruning: %w", f.path, err)
	}
	defer func() { _ = file.Close() }()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return PrunedTelemetry{}, fmt.Errorf("lock %s for pruning: %w", f.path, err)
	}
	info, err := file.Stat()
	if err != nil {
		return PrunedTelemetry{}, err
	}
	record := PrunedTelemetry{Time: time.Now().UTC(), Run: f.run, File: TelemetryFile, Bytes: info.Size(), Reason: fmt.Sprintf("telemetry total %d bytes exceeds budget %d", total, budget)}
	if err := appendDurable(r.PruningPath(), record); err != nil {
		return PrunedTelemetry{}, fmt.Errorf("record pruning before deletion: %w", err)
	}
	if err := os.Remove(f.path); err != nil {
		return PrunedTelemetry{}, fmt.Errorf("prune %s: %w", f.path, err)
	}
	return record, nil
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
