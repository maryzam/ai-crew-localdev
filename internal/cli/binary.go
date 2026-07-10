package cli

import "github.com/maryzam/ai-crew-localdev/internal/platform/binresolve"

func resolveOptionalBinary(name string) (string, error) {
	return binresolve.ResolveOptional(name)
}
