package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const product = "ai-crew"

func Data() (string, error) {
	return under("XDG_DATA_HOME", ".local", "share")
}

func Config() (string, error) {
	return under("XDG_CONFIG_HOME", ".config")
}

func Evidence() (string, error) {
	data, err := Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "evidence"), nil
}

func under(variable string, fallback ...string) (string, error) {
	base := os.Getenv(variable)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		base = filepath.Join(append([]string{home}, fallback...)...)
	}
	if !filepath.IsAbs(base) {
		return "", errors.New(variable + " must be absolute")
	}
	return filepath.Join(base, product), nil
}
