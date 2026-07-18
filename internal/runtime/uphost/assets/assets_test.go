package assets

import (
	"io/fs"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
)

const checkoutLangfuseDir = "../../../../contrib/langfuse"

func TestEmbeddedLangfuseAssetsMatchCheckout(t *testing.T) {
	langfuse, err := Langfuse()
	if err != nil {
		t.Fatal(err)
	}
	if err := embedasset.Parity(langfuse, checkoutLangfuseDir); err != nil {
		t.Fatalf("langfuse asset parity failed; run 'make langfuse-assets': %v", err)
	}
	for _, required := range []string{"docker-compose.yml", ".env.example"} {
		if _, err := fs.Stat(langfuse, required); err != nil {
			t.Fatalf("%s must be embedded for release installs: %v", required, err)
		}
	}
}
