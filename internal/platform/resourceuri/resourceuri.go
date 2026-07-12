package resourceuri

import (
	"fmt"
	"strings"
)

type URI struct {
	Provider   string
	Kind       string
	Identifier string
}

func Parse(value string) (URI, error) {
	provider, rest, ok := strings.Cut(value, ":")
	if !ok || provider == "" {
		return URI{}, fmt.Errorf("resource URI %q: missing provider", value)
	}
	kind, identifier, ok := strings.Cut(rest, ":")
	if !ok || kind == "" {
		return URI{}, fmt.Errorf("resource URI %q: missing kind", value)
	}
	if identifier == "" {
		return URI{}, fmt.Errorf("resource URI %q: missing identifier", value)
	}
	return URI{Provider: provider, Kind: kind, Identifier: identifier}, nil
}

func (uri URI) String() string {
	return uri.Provider + ":" + uri.Kind + ":" + uri.Identifier
}
