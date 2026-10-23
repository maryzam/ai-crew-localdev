package login

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
)

//go:embed assets/Containerfile
var loginContainerfile []byte

const imageRepository = "localhost/ai-crew-login"

type loginImage struct {
	ref string
	id  string
}

func imageRecipe(registry agents.Registry) (string, string) {
	all := registry.All()
	specs := make([]string, len(all))
	for index, agent := range all {
		specs[index] = agent.PackageSpec()
	}
	packages := strings.Join(specs, " ")
	digest := sha256.New()
	_, _ = digest.Write(loginContainerfile)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(packages))
	return imageRepository + ":" + hex.EncodeToString(digest.Sum(nil))[:24], packages
}

func (s *Service) prepareImage(ctx context.Context) (loginImage, error) {
	ref, packages := imageRecipe(s.registry)
	if s.podman(ctx, podmanHealthBudget, stdio{out: io.Discard, err: io.Discard}, nil, "image", "exists", ref) != nil {
		if err := s.buildImage(ctx, ref, packages); err != nil {
			return loginImage{}, err
		}
	}
	var output bytes.Buffer
	if err := s.podman(ctx, podmanHealthBudget, stdio{out: &output, err: s.console.Err}, nil, "image", "inspect", "--format", "{{.Id}}", ref); err != nil {
		return loginImage{}, fmt.Errorf("inspect login image: %w", err)
	}
	id := strings.TrimSpace(output.String())
	if id == "" {
		return loginImage{}, errors.New("login image has no immutable ID")
	}
	return loginImage{ref: ref, id: id}, nil
}

func (s *Service) buildImage(ctx context.Context, ref, packages string) error {
	contextDir, err := os.MkdirTemp("", "ai-login-image-")
	if err != nil {
		return fmt.Errorf("create login image context: %w", err)
	}
	defer func() { _ = os.RemoveAll(contextDir) }()
	if err := os.WriteFile(filepath.Join(contextDir, "Containerfile"), loginContainerfile, 0o600); err != nil {
		return fmt.Errorf("prepare login image: %w", err)
	}
	if err := s.podman(ctx, imageBuildBudget, stdio{out: s.console.Out, err: s.console.Err}, nil, "build", "--pull=missing", "--build-arg", "AGENT_PACKAGES="+packages, "--tag", ref, contextDir); err != nil {
		return fmt.Errorf("build pinned login image within %s: %w", imageBuildBudget, err)
	}
	return nil
}
