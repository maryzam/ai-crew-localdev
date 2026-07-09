package paths

import (
	"strings"
	"testing"
)

func TestBrokerSocketResolutionIsSymmetric(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	t.Setenv(EnvAuthSock, "")
	t.Setenv(EnvBrokerSocket, "")
	clientPath, source := BrokerClientSocket()
	if clientPath != DefaultSocketPath() || source != "" {
		t.Fatalf("unset envs: client = %q (%q), want default", clientPath, source)
	}
	if BrokerListenSocketPath() != clientPath {
		t.Fatalf("daemon %q and client %q disagree with no envs set", BrokerListenSocketPath(), clientPath)
	}

	t.Setenv(EnvBrokerSocket, " /custom/broker.sock ")
	clientPath, source = BrokerClientSocket()
	if clientPath != "/custom/broker.sock" || source != EnvBrokerSocket {
		t.Fatalf("operator socket: client = %q (%q), want the daemon socket", clientPath, source)
	}
	if BrokerListenSocketPath() != clientPath {
		t.Fatalf("daemon %q and client %q disagree when only %s is set", BrokerListenSocketPath(), clientPath, EnvBrokerSocket)
	}

	t.Setenv(EnvAuthSock, "/session/broker.sock")
	clientPath, source = BrokerClientSocket()
	if clientPath != "/session/broker.sock" || source != EnvAuthSock {
		t.Fatalf("session socket must win for clients: got %q (%q)", clientPath, source)
	}
	if BrokerListenSocketPath() != "/custom/broker.sock" {
		t.Fatalf("daemon listener must ignore the session env: %q", BrokerListenSocketPath())
	}
}

func TestPolicyAndEvidencePathResolversHonorEnvAndDefaults(t *testing.T) {
	t.Setenv(EnvConfigDir, t.TempDir())

	t.Setenv(EnvPolicyPath, "")
	if PolicyPath() != DefaultPolicyPath() {
		t.Fatalf("PolicyPath = %q, want default", PolicyPath())
	}
	t.Setenv(EnvPolicyPath, "~/custom/policy.json")
	if got := PolicyPath(); !strings.HasSuffix(got, "/custom/policy.json") || strings.HasPrefix(got, "~") {
		t.Fatalf("PolicyPath = %q, want home-expanded override", got)
	}

	t.Setenv(EnvAuditLog, " ")
	if AuditLogPath() != DefaultAuditLogPath() {
		t.Fatalf("AuditLogPath = %q, want default for blank env", AuditLogPath())
	}
	t.Setenv(EnvAuditLog, "/var/log/ai-agent-audit.log")
	if AuditLogPath() != "/var/log/ai-agent-audit.log" {
		t.Fatalf("AuditLogPath = %q, want override", AuditLogPath())
	}

	t.Setenv(EnvRunTelemetryLog, "")
	if RunTelemetryLogPath() != DefaultRunTelemetryPath() {
		t.Fatalf("RunTelemetryLogPath = %q, want default", RunTelemetryLogPath())
	}

	t.Setenv(EnvTelemetry, "disabled")
	if !TelemetryDisabled() {
		t.Fatal("TelemetryDisabled must honor the contract value")
	}
	t.Setenv(EnvTelemetry, "")
	if TelemetryDisabled() {
		t.Fatal("TelemetryDisabled true without the contract value")
	}
}
