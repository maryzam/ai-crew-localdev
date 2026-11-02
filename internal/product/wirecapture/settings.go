package wirecapture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/product/paths"
	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

const (
	settingsFile  = "wire-capture.json"
	settingsLimit = 4 << 10
)

type Settings struct {
	Enabled bool `json:"enabled"`
}

func settingsPath() (string, error) {
	config, err := paths.Config()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, settingsFile), nil
}

func LoadSettings() (Settings, error) {
	path, err := settingsPath()
	if err != nil {
		return Settings{}, err
	}
	data, err := securefile.ReadOwnerOnly(path, settingsLimit)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{Enabled: true}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("wire capture settings %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decoder.Decode(&stored); err != nil || stored.Enabled == nil {
		return Settings{}, fmt.Errorf("wire capture settings %s must hold {\"enabled\": true|false}; fix it or run 'ai capture on'", path)
	}
	return Settings{Enabled: *stored.Enabled}, nil
}

func SaveSettings(settings Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := securefile.EnsureOwnerOnlyDir(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return securefile.WriteOwnerOnly(path, append(data, '\n'))
}
