package assets

import (
	"io/fs"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
)

const checkoutPhoenixDir = "../../../../contrib/phoenix"

func TestEmbeddedPhoenixAssetsMatchCheckout(t *testing.T) {
	phoenix, err := Phoenix()
	if err != nil {
		t.Fatal(err)
	}
	if err := embedasset.Parity(phoenix, checkoutPhoenixDir); err != nil {
		t.Fatalf("phoenix asset parity failed; run 'make phoenix-assets': %v", err)
	}
	for _, required := range []string{"collector.yaml"} {
		if _, err := fs.Stat(phoenix, required); err != nil {
			t.Fatalf("%s must be embedded for release installs: %v", required, err)
		}
	}
}
