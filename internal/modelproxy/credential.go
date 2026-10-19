package modelproxy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"
)

const setupTokenPrefix = "sk-ant-oat01-"

var setupTokenShape = regexp.MustCompile("^" + setupTokenPrefix + "[A-Za-z0-9_-]+$")

type Credential struct {
	secret string
}

func (Credential) String() string   { return "[redacted]" }
func (Credential) GoString() string { return "modelproxy.Credential{[redacted]}" }

func (c Credential) Len() int { return len(c.secret) }

func LoadClaudeSetupToken(path string) (Credential, error) {
	raw, err := readOwnerOnlyFile(path, 4096)
	if err != nil {
		return Credential{}, err
	}
	token := strings.Join(strings.Fields(string(raw)), "")
	if !setupTokenShape.MatchString(token) || strings.Count(token, setupTokenPrefix) != 1 {
		return Credential{}, errors.New("credential file does not hold exactly one Claude setup-token")
	}
	return Credential{secret: token}, nil
}

func readOwnerOnlyFile(path string, limit int64) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open private credential file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > limit {
		return nil, errors.New("credential file must be a private regular file within its size limit")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("credential file must be owned by the broker user")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("cannot read private credential file")
	}
	return data, nil
}
