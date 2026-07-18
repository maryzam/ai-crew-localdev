package assets

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
)

const checkoutConfigDir = "../../../../.devcontainer"

func TestEmbeddedGenericAssetsMatchCheckout(t *testing.T) {
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	if err := embedasset.Parity(generic, checkoutConfigDir); err != nil {
		t.Fatalf("generic asset parity failed; run 'make devcontainer-assets': %v", err)
	}
}

func TestGenericImageBuildsFromStagedBinaryNotSource(t *testing.T) {
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := fs.ReadFile(generic, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	content := string(dockerfile)
	if !strings.Contains(content, "COPY bin/ai-agent /usr/local/bin/ai-agent") {
		t.Fatal("generic image must install the staged ai-agent binary from the build context")
	}
	for _, sourceBuild := range []string{"COPY . .", "COPY go.mod", "RUN make build", "go build"} {
		if strings.Contains(content, sourceBuild) {
			t.Fatalf("generic image must not build from a source checkout (found %q); a released binary has no source tree", sourceBuild)
		}
	}
}
