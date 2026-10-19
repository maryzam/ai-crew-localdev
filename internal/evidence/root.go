package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"golang.org/x/sys/unix"
)

const (
	DefaultTelemetryBudget int64  = 5 << 30
	MinFreeBytes           uint64 = 1 << 30
	TelemetryFile                 = "otel.jsonl"
	RunFile                       = "run.jsonl"
	AuditFile                     = "audit.jsonl"
	PruningFile                   = "pruning.jsonl"
	RunsDir                       = "runs"
	telemetryLockBudget           = 10 * time.Second
	lockRetryInterval             = 10 * time.Millisecond
)

var runNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
var ErrEvidenceBusy = errors.New("evidence directory lock timed out")

type Root struct {
	path string
}

func OpenRoot(path string) (*Root, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("evidence root %q must be absolute", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create evidence parent: %w", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create evidence root: %w", err)
	}
	if err := requireOwnerOnlyDir(path); err != nil {
		return nil, fmt.Errorf("evidence root %s: %w", path, err)
	}
	runs := filepath.Join(path, RunsDir)
	if err := os.Mkdir(runs, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create %s: %w", runs, err)
	}
	if err := requireOwnerOnlyDir(runs); err != nil {
		return nil, fmt.Errorf("evidence directory %s: %w", runs, err)
	}
	return &Root{path: path}, nil
}

func requireOwnerOnlyDir(path string) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("must be a directory, not a link or file")
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("must be owned by uid %d", os.Getuid())
	}
	if stat.Mode&0o077 != 0 {
		return fmt.Errorf("mode %04o grants group or other access", stat.Mode&0o777)
	}
	return nil
}

func (r *Root) Path() string { return r.path }

func (r *Root) RunDir(run string) (string, error) {
	if !runNamePattern.MatchString(run) {
		return "", fmt.Errorf("invalid run name %q", run)
	}
	return filepath.Join(r.path, RunsDir, run), nil
}

func (r *Root) TelemetryPath(run string) (string, error) {
	dir, err := r.RunDir(run)
	return filepath.Join(dir, TelemetryFile), err
}

func (r *Root) PruningPath() string {
	return filepath.Join(r.path, PruningFile)
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
	Time  time.Time `json:"time"`
	Event string    `json:"event"`
	Data  any       `json:"data,omitempty"`
}

func (r *Root) AppendRunEvent(run, event string, data any) error {
	dir, err := r.ensureRunDir(run)
	if err != nil {
		return err
	}
	return appendDurable(filepath.Join(dir, RunFile), RunEvent{Time: time.Now().UTC(), Event: event, Data: data})
}

func (r *Root) AppendTelemetry(run string, line []byte) error {
	dir, err := r.ensureRunDir(run)
	if err != nil {
		return err
	}
	lock, err := lockDirectory(dir, telemetryLockBudget)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	path := filepath.Join(dir, TelemetryFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	_, writeErr := f.Write(append(line, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("append %s: %w", path, writeErr)
	}
	return closeErr
}

func lockDirectory(path string, budget time.Duration) (*os.File, error) {
	dir, err := os.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open lock directory %s: %w", path, err)
	}
	deadline := time.Now().Add(budget)
	for {
		err := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return dir, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = dir.Close()
			return nil, fmt.Errorf("lock directory %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			_ = dir.Close()
			return nil, fmt.Errorf("%w: %s after %s", ErrEvidenceBusy, path, budget)
		}
		time.Sleep(lockRetryInterval)
	}
}

func (r *Root) ensureRunDir(run string) (string, error) {
	dir, err := r.RunDir(run)
	if err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create run directory: %w", err)
	}
	return dir, nil
}

func appendDurable(path string, record any) error {
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("append %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return securefile.SyncDirectory(filepath.Dir(path))
}
