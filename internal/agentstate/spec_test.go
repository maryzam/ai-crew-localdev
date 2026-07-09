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
		{Name: ".codex", Kind: Dir, Exclude: []string{""}},
		{Name: ".codex", Kind: Dir, Exclude: []string{"../packages"}},
		{Name: ".codex", Kind: Dir, Exclude: []string{"packages/current"}},
		{Name: ".codex", Kind: Dir, Exclude: []string{`packages\current`}},
	} {
		if err := ValidateSpecs([]Spec{spec}); err == nil {
			t.Fatalf("expected %v to be rejected", spec)
		}
	}
}

func TestSpecsReturnIndependentExcludeSlices(t *testing.T) {
	first := Specs()
	first[2].Exclude[0] = "changed"

	second := Specs()
	if second[2].Exclude[0] != "packages" {
		t.Fatalf("Specs shared exclude slice: %v", second[2].Exclude)
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
