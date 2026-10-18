package modelproxy

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
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
	info, err := os.Lstat(path)
	if err != nil {
		return Credential{}, err
	}
	if !info.Mode().IsRegular() {
		return Credential{}, fmt.Errorf("%s must be a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Credential{}, fmt.Errorf("%s must not be group or world accessible", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, err
	}
	token := strings.Join(strings.Fields(string(raw)), "")
	if !setupTokenShape.MatchString(token) || strings.Count(token, setupTokenPrefix) != 1 {
		return Credential{}, errors.New("credential file does not hold exactly one Claude setup-token")
	}
	return Credential{secret: token}, nil
}
