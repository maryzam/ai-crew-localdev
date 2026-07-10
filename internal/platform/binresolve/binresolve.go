package binresolve

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Resolver struct {
	Executable func() (string, error)
	LookPath   func(string) (string, error)
	Stat       func(string) (os.FileInfo, error)
	PathList   func() []string
}

func DefaultResolver() Resolver {
	return Resolver{
		Executable: os.Executable,
		LookPath:   exec.LookPath,
		Stat:       os.Stat,
		PathList:   func() []string { return filepath.SplitList(os.Getenv("PATH")) },
	}
}

func ResolveOptional(name string) (string, error) {
	return DefaultResolver().ResolveOptional(name)
}

func ResolveSibling(name string) (string, error) {
	return DefaultResolver().ResolveSibling(name)
}

func ResolveExecutableFromPath(name string, skipPath string) (string, error) {
	return DefaultResolver().ResolveExecutableFromPath(name, skipPath)
}

func IsExecutableFile(path string) bool {
	return DefaultResolver().IsExecutableFile(path)
}

func (r Resolver) ResolveOptional(name string) (string, error) {
	if p, err := r.ResolveSibling(name); err == nil {
		return p, nil
	}
	if p, err := r.lookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found", name)
}

func (r Resolver) ResolveSibling(name string) (string, error) {
	self, err := r.executable()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(filepath.Dir(self), name)
	info, err := r.stat(candidate)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", candidate)
	}
	if info.Mode()&0111 == 0 {
		return "", fmt.Errorf("%s is not executable", candidate)
	}
	return candidate, nil
}

func (r Resolver) ResolveExecutableFromPath(name string, skipPath string) (string, error) {
	var skipInfo os.FileInfo
	if skipPath != "" {
		if info, err := r.stat(skipPath); err == nil && !info.IsDir() {
			skipInfo = info
		}
	}
	for _, dir := range r.pathList() {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := r.stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if skipInfo != nil && os.SameFile(info, skipInfo) {
			continue
		}
		if info.Mode()&0111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}

func (r Resolver) IsExecutableFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := r.stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

func (r Resolver) executable() (string, error) {
	if r.Executable != nil {
		return r.Executable()
	}
	return os.Executable()
}

func (r Resolver) lookPath(name string) (string, error) {
	if r.LookPath != nil {
		return r.LookPath(name)
	}
	return exec.LookPath(name)
}

func (r Resolver) stat(path string) (os.FileInfo, error) {
	if r.Stat != nil {
		return r.Stat(path)
	}
	return os.Stat(path)
}

func (r Resolver) pathList() []string {
	if r.PathList != nil {
		return r.PathList()
	}
	return filepath.SplitList(os.Getenv("PATH"))
}
