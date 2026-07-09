package paths

import (
	"strings"
	"testing"
)

func TestBrokerSocketResolutionIsSymmetric(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	t.Setenv(EnvAuthSock, "")
	t.Setenv(EnvBrokerSocket, "")
	clientPath, source, err := BrokerClientSocket()
	if err != nil || clientPath != defaultSocketPath() || source != "" {
		t.Fatalf("unset envs: client = %q (%q, %v), want default", clientPath, source, err)
	}
	listenPath, err := BrokerListenSocketPath()
	if err != nil || listenPath != clientPath {
		t.Fatalf("daemon %q (%v) and client %q disagree with no envs set", listenPath, err, clientPath)
	}

	t.Setenv(EnvBrokerSocket, " /custom/broker.sock ")
	clientPath, source, err = BrokerClientSocket()
	if err != nil || clientPath != "/custom/broker.sock" || source != EnvBrokerSocket {
		t.Fatalf("operator socket: client = %q (%q, %v), want the daemon socket", clientPath, source, err)
	}
	listenPath, err = BrokerListenSocketPath()
	if err != nil || listenPath != clientPath {
		t.Fatalf("daemon %q (%v) and client %q disagree when only %s is set", listenPath, err, clientPath, EnvBrokerSocket)
	}

	t.Setenv(EnvAuthSock, "/session/broker.sock")
	clientPath, source, err = BrokerClientSocket()
	if err != nil || clientPath != "/session/broker.sock" || source != EnvAuthSock {
		t.Fatalf("session socket must win for clients: got %q (%q, %v)", clientPath, source, err)
	}
	if listenPath, err = BrokerListenSocketPath(); err != nil || listenPath != "/custom/broker.sock" {
		t.Fatalf("daemon listener must ignore the session env: %q (%v)", listenPath, err)
	}
}

func TestSocketResolversRejectRelativePaths(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	t.Setenv(EnvAuthSock, "")
	t.Setenv(EnvBrokerSocket, "relative/broker.sock")
	if _, err := BrokerListenSocketPath(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("daemon accepted a relative socket it would bind in its cwd: %v", err)
	}
	if _, _, err := BrokerClientSocket(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("client accepted a relative daemon socket: %v", err)
	}

	t.Setenv(EnvBrokerSocket, "")
	t.Setenv(EnvAuthSock, "relative/auth.sock")
	if _, _, err := BrokerClientSocket(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("client accepted a relative session socket: %v", err)
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
