package agentstate

import "testing"

func TestSpecsAreValidAndStructurallySafe(t *testing.T) {
	if err := ValidateSpecs(Specs()); err != nil {
		t.Fatalf("Specs are invalid: %v", err)
	}
}

func TestValidateSpecsRejectsUnsafeNames(t *testing.T) {
	for _, spec := range []Spec{
		{Name: "/abs", Kind: Dir},
		{Name: "../state", Kind: Dir},
		{Name: ".config/gh", Kind: Dir},
		{Name: `.config\gh`, Kind: Dir},
		{Name: ".ssh", Kind: Dir},
		{Name: ".gitconfig", Kind: File},
		{Name: ".codex", Kind: "other"},
	} {
		if err := ValidateSpecs([]Spec{spec}); err == nil {
			t.Fatalf("expected %v to be rejected", spec)
		}
	}
}

func TestValidateSpecsRejectsDuplicates(t *testing.T) {
	err := ValidateSpecs([]Spec{
		{Name: ".codex", Kind: Dir},
		{Name: ".codex", Kind: Dir},
	})
	if err == nil {
		t.Fatal("expected duplicate spec to be rejected")
	}
}
