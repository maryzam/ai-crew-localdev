package runenv

import (
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TestRequireManagedContainerAcceptsDevcontainerMarker(t *testing.T) {
	t.Setenv(paths.EnvContainer, "1")
	if err := RequireManagedContainer(); err != nil {
		t.Fatalf("RequireManagedContainer: %v", err)
	}
}

func TestRequireManagedContainerRejectsMissingMarker(t *testing.T) {
	t.Setenv(paths.EnvContainer, "")
	err := RequireManagedContainer()
	if err == nil || !strings.Contains(err.Error(), "devcontainer-only") {
		t.Fatalf("error = %v, want devcontainer marker refusal", err)
	}
}
