package cli

import "testing"

func TestNewRootConstructsFreshWorkflowCommands(t *testing.T) {
	first, err := NewRoot(setupTestServices)
	if err != nil {
		t.Fatal(err)
	}
	setup, _, err := first.Find([]string{"setup"})
	if err != nil {
		t.Fatal(err)
	}
	if err := setup.Flags().Set("agent", "first"); err != nil {
		t.Fatal(err)
	}

	second, err := NewRoot(setupTestServices)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"setup", "doctor", "up"} {
		command, _, err := second.Find([]string{name})
		if err != nil || command == second {
			t.Fatalf("missing %s command: %v", name, err)
		}
	}
	setup, _, err = second.Find([]string{"setup"})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := setup.Flags().GetString("agent"); err != nil || value != "" {
		t.Fatalf("fresh setup agent = %q, err = %v", value, err)
	}
}

func TestNewRootRejectsMissingProviderServices(t *testing.T) {
	if _, err := NewRoot(ProviderServices{}); err == nil {
		t.Fatal("expected missing provider services to fail")
	}
}
