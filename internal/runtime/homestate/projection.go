package homestate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/maryzam/ai-crew-localdev/internal/agentstate"
)

type Projection struct {
	realHome string
	runHome  string
	files    []projectedFile
	warnings []string
	closed   bool
	done     bool
}

type projectedFile struct {
	name     string
	realPath string
	runPath  string
	baseline []byte
	existed  bool
	mode     fs.FileMode
}

func Prepare(realHome string) (*Projection, error) {
	if err := agentstate.ValidateSpecs(agentstate.Specs()); err != nil {
		return nil, err
	}
	runHome, err := os.MkdirTemp("", "ai-agent-run-home-*")
	if err != nil {
		return nil, fmt.Errorf("create isolated home: %w", err)
	}
	projection := &Projection{realHome: realHome, runHome: runHome}
	if realHome == "" {
		return projection, nil
	}
	if !filepath.IsAbs(realHome) {
		_ = os.RemoveAll(runHome)
		return nil, fmt.Errorf("real home must be absolute")
	}
	if err := requireDirectory(realHome); err != nil {
		_ = os.RemoveAll(runHome)
		return nil, fmt.Errorf("inspect real home: %w", err)
	}
	for _, spec := range agentstate.Specs() {
		if spec.Kind == agentstate.Dir {
			if err := projection.prepareDir(spec.Name); err != nil {
				_ = os.RemoveAll(runHome)
				return nil, err
			}
			continue
		}
		if err := projection.prepareFile(spec.Name); err != nil {
			_ = os.RemoveAll(runHome)
			return nil, err
		}
	}
	return projection, nil
}

func (p *Projection) RunHome() string {
	return p.runHome
}

func (p *Projection) Commit() error {
	if p.done || p.realHome == "" {
		p.done = true
		return nil
	}
	p.done = true
	var errs []error
	for i := range p.files {
		if err := p.commitFile(&p.files[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Projection) Cleanup() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if p.runHome == "" {
		return nil
	}
	return os.RemoveAll(p.runHome)
}

func (p *Projection) Warnings() []string {
	return append([]string(nil), p.warnings...)
}

func (p *Projection) prepareDir(name string) error {
	realPath := filepath.Join(p.realHome, name)
	info, err := os.Lstat(realPath)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(realPath, 0o700); err != nil {
			return fmt.Errorf("prepare %s in real home: %w", name, err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect %s in real home: %w", name, err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s in real home is a symlink", name)
	} else if !info.IsDir() {
		return fmt.Errorf("%s in real home is not a directory", name)
	}
	if err := os.Symlink(realPath, filepath.Join(p.runHome, name)); err != nil {
		return fmt.Errorf("link %s into isolated home: %w", name, err)
	}
	return nil
}

func (p *Projection) prepareFile(name string) error {
	realPath := filepath.Join(p.realHome, name)
	runPath := filepath.Join(p.runHome, name)
	file := projectedFile{name: name, realPath: realPath, runPath: runPath, mode: 0o600}
	info, err := os.Lstat(realPath)
	if os.IsNotExist(err) {
		p.files = append(p.files, file)
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s in real home: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s in real home is a symlink", name)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s in real home is not a regular file", name)
	}
	data, err := os.ReadFile(realPath)
	if err != nil {
		return fmt.Errorf("read %s from real home: %w", name, err)
	}
	file.baseline = slices.Clone(data)
	file.existed = true
	file.mode = safeFileMode(info.Mode().Perm())
	if err := os.WriteFile(runPath, data, file.mode); err != nil {
		return fmt.Errorf("copy %s into isolated home: %w", name, err)
	}
	p.files = append(p.files, file)
	return nil
}

func (p *Projection) commitFile(file *projectedFile) error {
	info, err := os.Lstat(file.runPath)
	if os.IsNotExist(err) {
		if !file.existed {
			return nil
		}
		if !p.realMatchesBaseline(file) {
			p.warnings = append(p.warnings, fmt.Sprintf("%s changed in the real home while the run was active; removing the run copy wins", file.name))
		}
		if err := os.Remove(file.realPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s from real home: %w", file.name, err)
		}
		return syncDir(filepath.Dir(file.realPath))
	}
	if err != nil {
		return fmt.Errorf("inspect %s in isolated home: %w", file.name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s in isolated home is a symlink", file.name)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s in isolated home is not a regular file", file.name)
	}
	data, err := os.ReadFile(file.runPath)
	if err != nil {
		return fmt.Errorf("read %s from isolated home: %w", file.name, err)
	}
	if file.existed && bytes.Equal(data, file.baseline) {
		return nil
	}
	if !p.realMatchesBaseline(file) {
		p.warnings = append(p.warnings, fmt.Sprintf("%s changed in the real home while the run was active; the run copy wins", file.name))
	}
	if err := writeFileAtomically(file.realPath, data, safeFileMode(file.mode)); err != nil {
		return fmt.Errorf("persist %s to real home: %w", file.name, err)
	}
	return nil
}

func (p *Projection) realMatchesBaseline(file *projectedFile) bool {
	info, err := os.Lstat(file.realPath)
	if os.IsNotExist(err) {
		return !file.existed
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(file.realPath)
	return err == nil && bytes.Equal(data, file.baseline)
}

func requireDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func safeFileMode(mode fs.FileMode) fs.FileMode {
	if mode == 0 || mode&0o077 != 0 {
		return 0o600
	}
	return mode
}

func writeFileAtomically(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".ai-agent-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tmp)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	remove = false
	return syncDir(dir)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
