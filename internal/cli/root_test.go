package cli

import (
	"strings"
	"testing"
)

type rootFlagCase struct {
	command []string
	flag    string
	dirty   string
	fresh   string
}

func TestNewRootConstructsFreshWorkflowCommands(t *testing.T) {
	cases := []rootFlagCase{
		{command: []string{"setup"}, flag: "agent", dirty: "first", fresh: ""},
		{command: []string{"run"}, flag: "agent", dirty: "first", fresh: ""},
		{command: []string{"bootstrap"}, flag: "quiet", dirty: "true", fresh: "false"},
		{command: []string{"check"}, flag: "tail-lines", dirty: "5", fresh: "60"},
		{command: []string{"install"}, flag: "uninstall", dirty: "true", fresh: "false"},
		{command: []string{"policy", "init"}, flag: "draft", dirty: "true", fresh: "false"},
	}

	first, err := NewRoot(setupTestServices)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range cases {
		command, _, err := first.Find(tt.command)
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Flags().Set(tt.flag, tt.dirty); err != nil {
			t.Fatal(err)
		}
	}

	second, err := NewRoot(setupTestServices)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"setup", "doctor", "up", "run", "bootstrap", "check", "install"} {
		command, _, err := second.Find([]string{name})
		if err != nil || command == second {
			t.Fatalf("missing %s command: %v", name, err)
		}
	}

	for _, tt := range cases {
		command, _, err := second.Find(tt.command)
		if err != nil {
			t.Fatal(err)
		}
		flag := command.Flags().Lookup(tt.flag)
		if flag == nil {
			t.Fatalf("%s flag %s missing", strings.Join(tt.command, " "), tt.flag)
		}
		if value := flag.Value.String(); value != tt.fresh {
			t.Fatalf("fresh %s %s = %q, want %q", strings.Join(tt.command, " "), tt.flag, value, tt.fresh)
		}
	}
}

func TestNewRootRejectsMissingProviderServices(t *testing.T) {
	if _, err := NewRoot(ProviderServices{}); err == nil {
		t.Fatal("expected missing provider services to fail")
	}
}
