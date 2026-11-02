package evidence

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
	"golang.org/x/sys/unix"
)

const (
	DefaultTelemetryBudget int64  = 50 << 30
	MinFreeBytes           uint64 = 1 << 30
	TelemetryFile                 = "otel.jsonl"
	RunFile                       = "run.jsonl"
	AuditFile                     = "audit.jsonl"
	WireDir                       = "wire"
	RunsDir                       = "runs"
	pruningFile                   = "pruning.jsonl"
	runLockBudget                 = 2 * time.Second
)

var runName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func ValidateRun(run string) error {
	if !runName.MatchString(run) {
		return fmt.Errorf("run ID %q must be 1 to 64 letters, digits, or hyphens", run)
	}
	return nil
}

func (r *Root) RootPath() string { return r.path }

func (r *Root) RunDir(run string) (string, error) {
	if err := ValidateRun(run); err != nil {
		return "", err
	}
	return filepath.Join(r.path, RunsDir, run), nil
}

func (r *Root) TelemetryPath(run string) (string, error) {
	dir, err := r.RunDir(run)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, TelemetryFile), nil
}

func (r *Root) PruningPath() string {
	return filepath.Join(r.path, pruningFile)
}

func (r *Root) LatestRun() (string, error) {
	runs, err := r.Runs()
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", errors.New("no evidence runs found")
	}
	return runs[len(runs)-1], nil
}

func (r *Root) Runs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(r.path, RunsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list evidence runs: %w", err)
	}
	type runEntry struct {
		name     string
		activity time.Time
	}
	valid := make([]runEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !runName.MatchString(entry.Name()) {
			continue
		}
		activity, err := latestActivity(filepath.Join(r.path, RunsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("inspect evidence run %s: %w", entry.Name(), err)
		}
		valid = append(valid, runEntry{name: entry.Name(), activity: activity})
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].activity.Equal(valid[j].activity) {
			return valid[i].name < valid[j].name
		}
		return valid[i].activity.Before(valid[j].activity)
	})
	runs := make([]string, len(valid))
	for i, entry := range valid {
		runs[i] = entry.name
	}
	return runs, nil
}

func latestActivity(dir string) (time.Time, error) {
	var latest time.Time
	for _, name := range []string{RunFile, TelemetryFile} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return time.Time{}, err
		}
		if info.Mode().IsRegular() && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest, nil
}

func (r *Root) RequireFreeSpace(minimum uint64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(r.path, &stat); err != nil {
		return fmt.Errorf("inspect evidence volume: %w", err)
	}
	free := stat.Bavail * uint64(stat.Bsize)
	if free < minimum {
		return fmt.Errorf("evidence volume has %d bytes free, below the %d byte floor", free, minimum)
	}
	return nil
}

type RunEvent struct {
	Time  time.Time       `json:"time"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func (r *Root) AppendRunEvent(run, event string, data any) error {
	dir, err := r.ensureRunDir(run)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode run event: %w", err)
	}
	line, err := json.Marshal(RunEvent{Time: time.Now().UTC(), Event: event, Data: encoded})
	if err != nil {
		return fmt.Errorf("encode run event: %w", err)
	}
	return AppendLine(filepath.Join(dir, RunFile), line, true)
}

func (r *Root) RunEvents(run string, visit func(RunEvent)) (int, error) {
	dir, err := r.RunDir(run)
	if err != nil {
		return 0, err
	}
	return EachLine(filepath.Join(dir, RunFile), func(line []byte) error {
		var event RunEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		visit(event)
		return nil
	})
}

func (r *Root) AppendTelemetry(run string, line []byte) error {
	return r.WithRunLock(run, func(dir string) error {
		return AppendLine(filepath.Join(dir, TelemetryFile), line, false)
	})
}

func (r *Root) WithRunLock(run string, apply func(dir string) error) error {
	dir, err := r.ensureRunDir(run)
	if err != nil {
		return err
	}
	if err := r.RequireFreeSpace(MinFreeBytes); err != nil {
		return err
	}
	lock, err := securefile.LockDirectory(dir, runLockBudget)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	return apply(dir)
}

func (r *Root) ensureRunDir(run string) (string, error) {
	dir, err := r.RunDir(run)
	if err != nil {
		return "", err
	}
	_, statErr := os.Lstat(dir)
	if err := securefile.EnsureOwnerOnlyDir(filepath.Dir(dir)); err != nil {
		return "", err
	}
	if err := securefile.EnsureOwnerOnlyDir(dir); err != nil {
		return "", err
	}
	if errors.Is(statErr, os.ErrNotExist) {
		if err := securefile.SyncDirectory(filepath.Dir(dir)); err != nil {
			return "", err
		}
		if err := securefile.SyncDirectory(r.path); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func AppendLine(path string, line []byte, durable bool) error {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	complete, err := ownerOnlyAndComplete(file)
	if err != nil {
		return fmt.Errorf("evidence file %s: %w", path, err)
	}
	record := make([]byte, 0, len(line)+2)
	if !complete {
		record = append(record, '\n')
	}
	record = append(append(record, line...), '\n')
	if _, err := file.Write(record); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	if durable {
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync %s: %w", path, err)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if durable {
		return securefile.SyncDirectory(filepath.Dir(path))
	}
	return nil
}

func EachLine(path string, visit func([]byte) error) (int, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReaderSize(file, 1<<20)
	malformed := 0
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) > 0 {
				malformed++
			}
			return malformed, nil
		}
		if err != nil {
			return malformed, err
		}
		if len(line) > 1 && visit(line) != nil {
			malformed++
		}
	}
}
