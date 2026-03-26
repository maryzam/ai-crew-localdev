package devcontainer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntrypointMissingSocket(t *testing.T) {
	workspace := t.TempDir()
	sock := filepath.Join(t.TempDir(), "broker.sock")

	err := runEntrypoint(t, workspace, sock, commandStubs{
		socketType: "socket",
		socketMode: "600",
		socketUID:  currentUID(t),
		probeOK:    true,
	})
	if err == nil {
		t.Fatal("expected missing socket failure")
	}
	assertStderrContains(t, err, "broker socket not found")
}

func TestEntrypointRejectsWrongFileType(t *testing.T) {
	workspace := t.TempDir()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.WriteFile(sock, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	err := runEntrypoint(t, workspace, sock, commandStubs{
		socketType: "regular file",
		socketMode: "600",
		socketUID:  currentUID(t),
		probeOK:    true,
	})
	if err == nil {
		t.Fatal("expected wrong file type failure")
	}
	assertStderrContains(t, err, "expected a Unix socket")
}

func TestEntrypointRejectsWrongOwner(t *testing.T) {
	workspace := t.TempDir()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.WriteFile(sock, []byte("socket placeholder"), 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	err := runEntrypoint(t, workspace, sock, commandStubs{
		socketType: "socket",
		socketMode: "600",
		socketUID:  "99999",
		probeOK:    true,
	})
	if err == nil {
		t.Fatal("expected ownership failure")
	}
	assertStderrContains(t, err, "expected uid")
}

func TestEntrypointRejectsInsecureSocketPermissions(t *testing.T) {
	workspace := t.TempDir()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.WriteFile(sock, []byte("socket placeholder"), 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	err := runEntrypoint(t, workspace, sock, commandStubs{
		socketType: "socket",
		socketMode: "666",
		socketUID:  currentUID(t),
		probeOK:    true,
	})
	if err == nil {
		t.Fatal("expected permissions failure")
	}
	assertStderrContains(t, err, "owner-only permissions")
}

func TestEntrypointRejectsUnreadableSocket(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can bypass file readability checks")
	}

	workspace := t.TempDir()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.WriteFile(sock, []byte("socket placeholder"), 0o000); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	err := runEntrypoint(t, workspace, sock, commandStubs{
		socketType: "socket",
		socketMode: "600",
		socketUID:  currentUID(t),
		probeOK:    true,
	})
	if err == nil {
		t.Fatal("expected access failure")
	}
	assertStderrContains(t, err, "is not accessible")
}

func TestEntrypointRejectsUnreachableSocket(t *testing.T) {
	workspace := t.TempDir()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.WriteFile(sock, []byte("socket placeholder"), 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	err := runEntrypoint(t, workspace, sock, commandStubs{
		socketType: "socket",
		socketMode: "600",
		socketUID:  currentUID(t),
		probeOK:    false,
	})
	if err == nil {
		t.Fatal("expected broker probe failure")
	}
	assertStderrContains(t, err, "not accepting connections")
}

func TestEntrypointRequiresWritableWorkspace(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o500); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	sock := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.WriteFile(sock, []byte("socket placeholder"), 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	err := runEntrypoint(t, workspace, sock, commandStubs{
		socketType: "socket",
		socketMode: "600",
		socketUID:  currentUID(t),
		probeOK:    true,
	})
	if err == nil {
		t.Fatal("expected workspace failure")
	}
	assertStderrContains(t, err, "workspace directory")
}

func TestEntrypointHealthyStartup(t *testing.T) {
	workspace := t.TempDir()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	if err := os.WriteFile(sock, []byte("socket placeholder"), 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	cmd := exec.Command("bash", scriptPath(), "sh", "-c", "printf ok")
	cmd.Env = append(os.Environ(),
		"AI_AGENT_AUTH_SOCK="+sock,
		"AI_AGENT_WORKSPACE_DIR="+workspace,
	)
	stubs := installCommandStubs(t, commandStubs{
		socketType: "socket",
		socketMode: "600",
		socketUID:  currentUID(t),
		probeOK:    true,
	})
	cmd.Env = append(cmd.Env, "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("healthy entrypoint failed: %v\n%s", err, out)
	}
	if string(out) != "ok" {
		t.Fatalf("stdout = %q, want %q", string(out), "ok")
	}
}

func runEntrypoint(t *testing.T, workspace, sock string, stubs commandStubs) error {
	t.Helper()

	cmd := exec.Command("bash", scriptPath(), "true")
	cmd.Env = append(os.Environ(),
		"AI_AGENT_AUTH_SOCK="+sock,
		"AI_AGENT_WORKSPACE_DIR="+workspace,
	)
	stubDir := installCommandStubs(t, stubs)
	cmd.Env = append(cmd.Env, "PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return wrapExecError(err, out)
}

func assertStderrContains(t *testing.T, err error, want string) {
	t.Helper()
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

func currentUID(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("id", "-u")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("id -u: %v", err)
	}
	return strings.TrimSpace(string(out))
}

type commandStubs struct {
	socketType string
	socketMode string
	socketUID  string
	probeOK    bool
}

func installCommandStubs(t *testing.T, stubs commandStubs) string {
	t.Helper()

	dir := t.TempDir()
	statScript := filepath.Join(dir, "stat")
	pythonScript := filepath.Join(dir, "python3")

	statBody := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  %F) printf '%s\\n' '" + stubs.socketType + "' ;;\n" +
		"  %u) printf '%s\\n' '" + stubs.socketUID + "' ;;\n" +
		"  %g) printf '%s\\n' '1000' ;;\n" +
		"  %a) printf '%s\\n' '" + stubs.socketMode + "' ;;\n" +
		"  *) echo unsupported stat format >&2; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(statScript, []byte(statBody), 0o755); err != nil {
		t.Fatalf("write stat stub: %v", err)
	}

	pythonBody := "#!/bin/sh\n"
	if stubs.probeOK {
		pythonBody += "exit 0\n"
	} else {
		pythonBody += "exit 1\n"
	}
	if err := os.WriteFile(pythonScript, []byte(pythonBody), 0o755); err != nil {
		t.Fatalf("write python stub: %v", err)
	}

	return dir
}

func scriptPath() string {
	return filepath.Join("..", "..", ".devcontainer", "entrypoint.sh")
}

func wrapExecError(err error, output []byte) error {
	return execError{cause: err, output: strings.TrimSpace(string(output))}
}

type execError struct {
	cause  error
	output string
}

func (e execError) Error() string {
	if e.output == "" {
		return e.cause.Error()
	}
	return e.output
}
