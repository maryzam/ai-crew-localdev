package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
	"golang.org/x/sys/unix"
)

var journalName = regexp.MustCompile(`^[a-z][a-z0-9-]*\.jsonl$`)

var ErrIncompleteRecord = errors.New("evidence journal ends with an incomplete record")

type Root struct {
	path string
}

func Open(path string) (*Root, error) {
	if err := securefile.EnsureOwnerOnlyDir(path); err != nil {
		return nil, fmt.Errorf("evidence root: %w", err)
	}
	return &Root{path: path}, nil
}

func (r *Root) Path(journal string) (string, error) {
	if !journalName.MatchString(journal) {
		return "", fmt.Errorf("invalid evidence journal %q", journal)
	}
	return filepath.Join(r.path, journal), nil
}

func (r *Root) Append(journal string, record any) error {
	path, err := r.Path(journal)
	if err != nil {
		return err
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode evidence record: %w", err)
	}
	line = append(line, '\n')
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open evidence journal %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	if err := requireCompleteOwnerOnlyJournal(file); err != nil {
		return fmt.Errorf("evidence journal %s: %w", path, err)
	}
	written, err := file.Write(line)
	if err == nil && written != len(line) {
		err = errors.New("short write")
	}
	if err != nil {
		return fmt.Errorf("append evidence journal %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync evidence journal %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close evidence journal %s: %w", path, err)
	}
	return securefile.SyncDirectory(r.path)
}

func requireCompleteOwnerOnlyJournal(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Getuid()) || stat.Mode&0o077 != 0 {
		return errors.New("must be an owner-only regular file")
	}
	if stat.Size == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, stat.Size-1); err != nil {
		return err
	}
	if last[0] != '\n' {
		return ErrIncompleteRecord
	}
	return nil
}

func (r *Root) Records(journal string, limit int64) ([]json.RawMessage, error) {
	path, err := r.Path(journal)
	if err != nil {
		return nil, err
	}
	data, err := securefile.ReadOwnerOnly(path, limit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read evidence journal %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	if data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("evidence journal %s: %w", path, ErrIncompleteRecord)
	}
	lines := bytes.Split(data[:len(data)-1], []byte("\n"))
	records := make([]json.RawMessage, len(lines))
	for index, line := range lines {
		if !json.Valid(line) {
			return nil, fmt.Errorf("evidence journal %s has a malformed record at line %d", path, index+1)
		}
		records[index] = json.RawMessage(line)
	}
	return records, nil
}
