package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
)

const (
	DirName          = ".ai-agent"
	FileName         = "manifest.json"
	maxManifestBytes = 1 << 20
)

func PathIn(root string) string {
	return filepath.Join(root, DirName, FileName)
}

func Find(root string) (string, bool) {
	path := PathIn(root)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

func Load(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read project manifest: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read project manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("project manifest %s exceeds %d bytes", path, maxManifestBytes)
	}
	return Parse(data)
}

func Parse(data []byte) (*File, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var f File
	if err := decoder.Decode(&f); err != nil {
		return nil, fmt.Errorf("failed to parse project manifest (schema %q accepts only declared fields): %w", schema.ManifestSchemaV1, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("failed to parse project manifest: trailing content after JSON document")
	}
	return &f, nil
}
