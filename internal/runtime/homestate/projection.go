package homestate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/agentstate"
)

type Projection struct {
	realHome string
	runHome  string
	dirs     []projectedDir
	files    []projectedFile
	warnings []string
	closed   bool
	done     bool
}

type projectedDir struct {
	name            string
	realPath        string
	runPath         string
	existed         bool
	baseline        string
	skippedSymlinks map[string]string
	exclude         []string
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
			if err := projection.prepareDir(spec); err != nil {
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
	for i := range p.dirs {
		if err := p.commitDir(&p.dirs[i]); err != nil {
			errs = append(errs, err)
		}
	}
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

func (p *Projection) prepareDir(spec agentstate.Spec) error {
	name := spec.Name
	realPath := filepath.Join(p.realHome, name)
	runPath := filepath.Join(p.runHome, name)
	dir := projectedDir{name: name, realPath: realPath, runPath: runPath, exclude: append([]string(nil), spec.Exclude...)}
	info, err := os.Lstat(realPath)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(runPath, 0o700); err != nil {
			return fmt.Errorf("prepare %s in isolated home: %w", name, err)
		}
		baseline, err := dirDigest(runPath, dir.exclude)
		if err != nil {
			return fmt.Errorf("snapshot %s in isolated home: %w", name, err)
		}
		dir.baseline = baseline
		p.dirs = append(p.dirs, dir)
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect %s in real home: %w", name, err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s in real home is a symlink", name)
	} else if !info.IsDir() {
		return fmt.Errorf("%s in real home is not a directory", name)
	}
	baseline, err := dirDigest(realPath, dir.exclude)
	if err != nil {
		return fmt.Errorf("snapshot %s in real home: %w", name, err)
	}
	dir.existed = true
	dir.baseline = baseline
	skippedSymlinks, err := copyDirSnapshot(realPath, runPath, dir.exclude)
	if err != nil {
		return fmt.Errorf("copy %s into isolated home: %w", name, err)
	}
	dir.skippedSymlinks = skippedSymlinks
	p.dirs = append(p.dirs, dir)
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

func (p *Projection) commitDir(dir *projectedDir) error {
	info, err := os.Lstat(dir.runPath)
	if os.IsNotExist(err) {
		if !dir.existed {
			return nil
		}
		if !p.realDirMatchesBaseline(dir) {
			p.warnings = append(p.warnings, fmt.Sprintf("%s changed in the real home while the run was active; removing the run copy wins", dir.name))
		}
		if err := removeDirState(dir.realPath); err != nil {
			return fmt.Errorf("remove %s from real home: %w", dir.name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s in isolated home: %w", dir.name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s in isolated home is a symlink", dir.name)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s in isolated home is not a directory", dir.name)
	}
	if err := validateDirTree(dir.runPath, dir.exclude); err != nil {
		return fmt.Errorf("validate %s in isolated home: %w", dir.name, err)
	}
	runDigest, err := dirDigest(dir.runPath, dir.exclude)
	if err != nil {
		return fmt.Errorf("snapshot %s in isolated home: %w", dir.name, err)
	}
	if runDigest == dir.baseline {
		return nil
	}
	if !p.realDirMatchesBaseline(dir) {
		p.warnings = append(p.warnings, fmt.Sprintf("%s changed in the real home while the run was active; the run copy wins", dir.name))
	}
	if err := replaceDirState(dir.realPath, dir.runPath, dir.skippedSymlinks, dir.exclude); err != nil {
		return fmt.Errorf("persist %s to real home: %w", dir.name, err)
	}
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

func (p *Projection) realDirMatchesBaseline(dir *projectedDir) bool {
	if !dir.existed {
		if _, err := os.Lstat(dir.realPath); os.IsNotExist(err) {
			return true
		}
		return false
	}
	digest, err := dirDigest(dir.realPath, dir.exclude)
	return err == nil && digest == dir.baseline
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
	if mode == 0 {
		return 0o600
	}
	if mode&0o100 != 0 {
		return 0o700
	}
	if mode&0o077 != 0 {
		return 0o600
	}
	return mode
}

func safeDirMode(mode fs.FileMode) fs.FileMode {
	if mode == 0 || mode&0o077 != 0 {
		return 0o700
	}
	return mode
}

func copyDirSnapshot(src string, dst string, exclude []string) (map[string]string, error) {
	skippedSymlinks := map[string]string{}
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := safeRel(src, path)
		if err != nil {
			return err
		}
		if isExcluded(rel, exclude) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			skippedSymlinks[rel] = target
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, safeDirMode(info.Mode().Perm()))
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, safeFileMode(info.Mode().Perm()))
	})
	if err != nil {
		return nil, err
	}
	return skippedSymlinks, nil
}

func copyDirStrict(src string, dst string, exclude []string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", path)
		}
		rel, err := safeRel(src, path)
		if err != nil {
			return err
		}
		if isExcluded(rel, exclude) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, safeDirMode(info.Mode().Perm()))
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, safeFileMode(info.Mode().Perm()))
	})
}

func validateDirTree(root string, exclude []string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", path)
		}
		if info.IsDir() || info.Mode().IsRegular() {
			rel, err := safeRel(root, path)
			if err != nil {
				return err
			}
			if isExcluded(rel, exclude) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return nil
		}
		return fmt.Errorf("%s is not a regular file or directory", path)
	})
}

func dirDigest(root string, exclude []string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := safeRel(root, path)
		if err != nil {
			return err
		}
		if isExcluded(rel, exclude) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(info.Mode().String()))
		_, _ = h.Write([]byte{0})
		if info.IsDir() {
			_, _ = h.Write([]byte("dir"))
			_, _ = h.Write([]byte{0})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isExcluded(rel string, exclude []string) bool {
	for _, name := range exclude {
		if rel == name || strings.HasPrefix(rel, name+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func safeRel(root string, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if rel == "" {
		return ".", nil
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s escapes %s", path, root)
	}
	return rel, nil
}

func replaceDirState(realPath string, runPath string, skippedSymlinks map[string]string, exclude []string) error {
	parent := filepath.Dir(realPath)
	base := filepath.Base(realPath)
	stage, err := os.MkdirTemp(parent, "."+base+".ai-agent-stage-*")
	if err != nil {
		return err
	}
	stageReady := false
	defer func() {
		if !stageReady {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := copyDirStrict(runPath, stage, exclude); err != nil {
		return err
	}
	if err := restoreSkippedSymlinks(stage, skippedSymlinks); err != nil {
		return err
	}
	if err := syncTree(stage); err != nil {
		return err
	}
	if err := ensureReplaceableDir(realPath); err != nil {
		return err
	}
	backup := ""
	if _, err := os.Lstat(realPath); err == nil {
		backup, err = unusedTempPath(parent, "."+base+".ai-agent-backup-*")
		if err != nil {
			return err
		}
		if err := os.Rename(realPath, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	preserved := []string(nil)
	if backup != "" {
		preserved, err = moveExcludedEntries(backup, stage, exclude)
		if err != nil {
			return restoreDirBackup(realPath, backup, stage, preserved, err)
		}
		if err := syncTree(stage); err != nil {
			return restoreDirBackup(realPath, backup, stage, preserved, err)
		}
	}
	if err := os.Rename(stage, realPath); err != nil {
		if backup != "" {
			return restoreDirBackup(realPath, backup, stage, preserved, err)
		}
		return err
	}
	stageReady = true
	if err := syncDir(parent); err != nil {
		return err
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return err
		}
		return syncDir(parent)
	}
	return nil
}

func moveExcludedEntries(srcRoot string, dstRoot string, exclude []string) ([]string, error) {
	moved := []string(nil)
	for _, name := range exclude {
		src := filepath.Join(srcRoot, name)
		dst := filepath.Join(dstRoot, name)
		if _, err := os.Lstat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return moved, err
		}
		if err := os.RemoveAll(dst); err != nil {
			return moved, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return moved, err
		}
		if err := os.Rename(src, dst); err != nil {
			return moved, err
		}
		moved = append(moved, name)
	}
	return moved, nil
}

func restoreDirBackup(realPath string, backup string, stage string, preserved []string, cause error) error {
	for i := len(preserved) - 1; i >= 0; i-- {
		name := preserved[i]
		src := filepath.Join(stage, name)
		dst := filepath.Join(backup, name)
		if _, err := os.Lstat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("%w; inspect preserved %s: %v", cause, name, err)
		}
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("%w; prepare preserved restore %s: %v", cause, name, err)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("%w; restore preserved %s: %v", cause, name, err)
		}
	}
	if restoreErr := os.Rename(backup, realPath); restoreErr != nil {
		return fmt.Errorf("%w; restore %s: %v", cause, realPath, restoreErr)
	}
	return cause
}

func restoreSkippedSymlinks(stage string, symlinks map[string]string) error {
	for rel, target := range symlinks {
		if rel == "." {
			continue
		}
		path := filepath.Join(stage, rel)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.Symlink(target, path); err != nil {
			return err
		}
	}
	return nil
}

func removeDirState(realPath string) error {
	if err := ensureReplaceableDir(realPath); err != nil {
		return err
	}
	if err := os.RemoveAll(realPath); err != nil {
		return err
	}
	return syncDir(filepath.Dir(realPath))
}

func ensureReplaceableDir(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
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

func unusedTempPath(dir string, pattern string) (string, error) {
	path, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
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

func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return syncDir(path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	})
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
