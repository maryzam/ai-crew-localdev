package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

func Find(root string) (string, bool, error) {
	path := PathIn(root)
	err := requireRegularFile(path)
	switch {
	case err == nil:
		return path, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	default:
		return "", false, err
	}
}

func Load(path string) (*File, error) {
	if err := requireRegularFile(path); err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read project manifest: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to read project manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("project manifest %s is not a regular file", path)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read project manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("project manifest %s exceeds %d bytes", path, maxManifestBytes)
	}
	return Parse(data)
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("failed to read project manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("project manifest %s must be a regular file, not %s", path, info.Mode().Type())
	}
	return nil
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
