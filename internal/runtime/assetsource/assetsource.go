package assetsource

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TrustedCheckoutDir() (string, bool) {
	dir := strings.TrimSpace(os.Getenv(paths.EnvDevAssetsDir))
	if dir == "" {
		return "", false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

func FS(embedded fs.FS, checkoutSubdir string) fs.FS {
	if dir, ok := TrustedCheckoutDir(); ok {
		sub := filepath.Join(dir, checkoutSubdir)
		if info, err := os.Stat(sub); err == nil && info.IsDir() {
			return os.DirFS(sub)
		}
	}
	return embedded
}
