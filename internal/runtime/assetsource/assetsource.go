package assetsource

import (
	"os"
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
