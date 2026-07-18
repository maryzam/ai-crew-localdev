package readiness

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

func TestDoctorPEMVerdictMatchesBrokerAcceptance(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.pem")
	if err := os.WriteFile(good, []byte("pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	groupReadable := filepath.Join(dir, "group.pem")
	if err := os.WriteFile(groupReadable, []byte("pem"), 0o640); err != nil {
		t.Fatal(err)
	}
	symlinked := filepath.Join(dir, "link.pem")
	if err := os.Symlink(good, symlinked); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(dir, "dir.pem")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(dir, "big.pem")
	if err := os.WriteFile(oversized, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.pem")

	service := New(Dependencies{ExpandPath: func(path string) string { return path }, Now: time.Now})
	for _, keyPath := range []string{good, groupReadable, symlinked, directory, oversized, missing} {
		_, brokerErr := securefile.ReadOwnerOnly(keyPath, 1<<20)
		brokerAccepts := brokerErr == nil
		identities := identity.IdentitiesFile{SchemaVersion: schema.IdentitiesSchemaV2, Agents: map[string]identity.AgentIdentity{"agent": {AppKey: keyPath, AppID: "1", GitName: "agent", GitEmail: "agent@example.test"}}}
		doctorReady := service.IdentityKeys(identities)[0].Status == StatusPass
		if brokerAccepts != doctorReady {
			t.Fatalf("%s: broker accepts=%v but doctor ready=%v; doctor must not approximate broker acceptance", keyPath, brokerAccepts, doctorReady)
		}
	}
}
