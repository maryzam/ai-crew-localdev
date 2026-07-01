package correlation

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	RunIDPrefix      = "run_"
	RunIDHexLength   = 32
	MaxRunIDLength   = 64
	MaxTaskRefLength = 200
)

func ValidateRunID(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > MaxRunIDLength || !strings.HasPrefix(value, RunIDPrefix) || len(value) == len(RunIDPrefix) {
		return fmt.Errorf("run ID must use the %s prefix and at most %d printable ASCII characters", RunIDPrefix, MaxRunIDLength)
	}
	for _, r := range value {
		if r > unicode.MaxASCII || unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("run ID must use the %s prefix and at most %d printable ASCII characters", RunIDPrefix, MaxRunIDLength)
		}
	}
	return nil
}

func ValidateTaskRef(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > MaxTaskRefLength {
		return fmt.Errorf("task reference exceeds %d characters", MaxTaskRefLength)
	}
	for _, r := range value {
		if r > unicode.MaxASCII || unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("task reference must contain printable ASCII without whitespace")
		}
	}
	return nil
}
