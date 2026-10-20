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

const (
	phoenixPullBudget  = 5 * time.Minute
	phoenixStartBudget = 90 * time.Second
)

func StartObservability(ctx context.Context, runtime string, streams Streams, progress ProgressFunc) error {
	return startObservability(ctx, runtime, streams, progress, runCommand)
}

func startObservability(ctx context.Context, runtime string, streams Streams, progress ProgressFunc, runner CommandRunner) error {
	composePath, err := findPhoenixCompose()
	if err != nil {
		return err
	}
	report(progress, Progress{Kind: PhoenixStarting})
	pullContext, cancelPull := context.WithTimeout(ctx, phoenixPullBudget)
	err = runner(pullContext, runtime, []string{"compose", "-f", composePath, "pull"}, Streams{Out: streams.Out, Err: streams.Err})
	cancelPull()
	if err != nil {
		return fmt.Errorf("%s compose pull: %w", runtime, err)
	}
	startContext, cancelStart := context.WithTimeout(ctx, phoenixStartBudget)
	defer cancelStart()
	if err := runner(startContext, runtime, []string{"compose", "-f", composePath, "up", "-d", "--wait"}, Streams{Out: streams.Out, Err: streams.Err}); err != nil {
		return fmt.Errorf("%s compose up: %w", runtime, err)
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
