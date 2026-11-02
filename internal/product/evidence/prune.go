package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

var ErrTelemetryBudgetExceeded = errors.New("telemetry budget exceeded by active runs")

type PrunedTelemetry struct {
	Time   time.Time `json:"time"`
	Run    string    `json:"run"`
	File   string    `json:"file"`
	Bytes  int64     `json:"bytes"`
	Reason string    `json:"reason"`
}

type prunable struct {
	run      string
	name     string
	path     string
	size     int64
	modified time.Time
}

func (r *Root) PruneTelemetry(budget int64, active map[string]bool) ([]PrunedTelemetry, error) {
	runs := filepath.Join(r.path, RunsDir)
	if err := securefile.EnsureOwnerOnlyDir(runs); err != nil {
		return nil, err
	}
	lock, err := securefile.LockDirectory(runs, runLockBudget)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	items, total, err := r.prunables()
	if err != nil {
		return nil, err
	}
	var activeBytes int64
	for _, item := range items {
		if active[item.run] {
			activeBytes += item.size
		}
	}
	if activeBytes > budget {
		return nil, fmt.Errorf("%w: active runs hold %d bytes against a %d byte budget", ErrTelemetryBudgetExceeded, activeBytes, budget)
	}
	var pruned []PrunedTelemetry
	for _, item := range items {
		if total <= budget {
			return pruned, nil
		}
		if active[item.run] {
			continue
		}
		record, err := r.prune(item, total, budget)
		if err != nil {
			return pruned, err
		}
		total -= record.Bytes
		pruned = append(pruned, record)
	}
	return pruned, nil
}

func (r *Root) prune(item prunable, total, budget int64) (PrunedTelemetry, error) {
	lock, err := securefile.LockDirectory(filepath.Dir(item.path), runLockBudget)
	if err != nil {
		return PrunedTelemetry{}, err
	}
	defer func() { _ = lock.Close() }()
	size, _, err := measure(item.path)
	if err != nil {
		return PrunedTelemetry{}, fmt.Errorf("inspect %s for pruning: %w", item.path, err)
	}
	record := PrunedTelemetry{Time: time.Now().UTC(), Run: item.run, File: item.name, Bytes: size, Reason: fmt.Sprintf("telemetry total %d bytes exceeds budget %d", total, budget)}
	line, err := json.Marshal(record)
	if err != nil {
		return PrunedTelemetry{}, err
	}
	if err := AppendLine(r.PruningPath(), line, true); err != nil {
		return PrunedTelemetry{}, fmt.Errorf("record pruning before deletion: %w", err)
	}
	if err := os.RemoveAll(item.path); err != nil {
		return PrunedTelemetry{}, fmt.Errorf("prune %s: %w", item.path, err)
	}
	return record, nil
}

func (r *Root) prunables() ([]prunable, int64, error) {
	entries, err := os.ReadDir(filepath.Join(r.path, RunsDir))
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	var items []prunable
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() || !runName.MatchString(entry.Name()) {
			continue
		}
		for _, name := range []string{TelemetryFile, WireDir} {
			path := filepath.Join(r.path, RunsDir, entry.Name(), name)
			size, modified, err := measure(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, 0, err
			}
			items = append(items, prunable{run: entry.Name(), name: name, path: path, size: size, modified: modified})
			total += size
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].modified.Equal(items[j].modified) {
			return items[i].path < items[j].path
		}
		return items[i].modified.Before(items[j].modified)
	})
	return items, total, nil
}

func measure(path string) (int64, time.Time, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	if info.Mode().IsRegular() {
		return info.Size(), info.ModTime(), nil
	}
	if !info.IsDir() {
		return 0, time.Time{}, fmt.Errorf("%s is neither a regular file nor a directory", path)
	}
	var size int64
	modified := info.ModTime()
	err = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		file, err := entry.Info()
		if err != nil {
			return err
		}
		size += file.Size()
		if file.ModTime().After(modified) {
			modified = file.ModTime()
		}
		return nil
	})
	return size, modified, err
}

func Size(path string) (int64, error) {
	size, _, err := measure(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return size, err
}
