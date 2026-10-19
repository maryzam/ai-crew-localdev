package uphost

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/assetsource"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost/assets"
)

func StartObservability(ctx context.Context, streams Streams, progress ProgressFunc) error {
	composePath, err := findPhoenixCompose()
	if err != nil {
		return err
	}
	report(progress, Progress{Kind: PhoenixStarting})
	startContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := runCommand(startContext, "docker", []string{"compose", "-f", composePath, "up", "-d", "--wait"}, Streams{Out: streams.Out, Err: streams.Err}); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}
	report(progress, Progress{Kind: PhoenixReady})
	return nil
}

func findPhoenixCompose() (string, error) {
	if dir, ok := assetsource.TrustedCheckoutDir(); ok {
		candidate := filepath.Join(dir, "contrib", "phoenix", "docker-compose.yml")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return preparePhoenixCompose(paths.DataDir())
}

func preparePhoenixCompose(dataDir string) (string, error) {
	phoenix, err := assets.Phoenix()
	if err != nil {
		return "", fmt.Errorf("load Phoenix assets: %w", err)
	}
	composeDir := filepath.Join(dataDir, assets.PhoenixDir)
	if err := embedasset.Materialize(phoenix, composeDir, func(string) fs.FileMode { return 0o600 }); err != nil {
		return "", err
	}
	return filepath.Join(composeDir, "docker-compose.yml"), nil
}

func report(sink ProgressFunc, progress Progress) {
	if sink != nil {
		sink(progress)
	}
}
