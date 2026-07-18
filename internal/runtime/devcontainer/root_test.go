package devcontainer

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ai-agent")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareGenericRootNeedsNoCheckout(t *testing.T) {
	dataDir := t.TempDir()
	self := fakeBinary(t, "installed-binary")

	root, err := PrepareGenericRoot(dataDir, "/home/me/projA", func() (string, error) { return self, nil })
	if err != nil {
		t.Fatalf("prepare generic root: %v", err)
	}
	if want := GenericRootPath(dataDir, "/home/me/projA"); root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}

	for _, name := range []string{"Dockerfile", "devcontainer.json", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(root, configDirName, name)); err != nil {
			t.Fatalf("%s missing from prepared context: %v", name, err)
		}
	}

	entrypoint, err := os.Stat(filepath.Join(root, configDirName, "entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if entrypoint.Mode().Perm() != 0o755 {
		t.Fatalf("entrypoint mode = %v, want 0755", entrypoint.Mode().Perm())
	}

	binary := filepath.Join(root, binaryDirName, binaryTargetName)
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read staged binary: %v", err)
	}
	if string(content) != "installed-binary" {
		t.Fatalf("staged binary = %q, want the running ai-agent binary", content)
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("staged binary mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestPrepareGenericRootRestagesUpgradedBinary(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := PrepareGenericRoot(dataDir, "/ws", func() (string, error) { return fakeBinary(t, "old"), nil }); err != nil {
		t.Fatalf("prepare generic root: %v", err)
	}
	root, err := PrepareGenericRoot(dataDir, "/ws", func() (string, error) { return fakeBinary(t, "new"), nil })
	if err != nil {
		t.Fatalf("re-prepare generic root: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, binaryDirName, binaryTargetName))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Fatalf("staged binary = %q, want the upgraded binary", content)
	}
}

func TestGenericRootPathIsWorkspaceScoped(t *testing.T) {
	data := t.TempDir()
	if GenericRootPath(data, "/home/me/projA") == GenericRootPath(data, "/home/me/projB") {
		t.Fatal("distinct workspaces must not share a devcontainer identity")
	}
	if GenericRootPath(data, "/home/me/projA") != GenericRootPath(data, "/home/me/projA/") {
		t.Fatal("the same workspace must map to a stable identity")
	}
}

func TestGenericRootPathCanonicalizesSymlinkedWorkspace(t *testing.T) {
	data := t.TempDir()
	real := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-workspace")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if GenericRootPath(data, real) != GenericRootPath(data, link) {
		t.Fatal("a symlinked workspace must map to the same identity as its target")
	}
}

func TestPrepareGenericRootHonorsTrustedCheckout(t *testing.T) {
	checkout := t.TempDir()
	config := filepath.Join(checkout, ".devcontainer")
	if err := os.MkdirAll(config, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"Dockerfile": "FROM custom", "devcontainer.json": "{}", "entrypoint.sh": "#!/bin/sh\n"} {
		if err := os.WriteFile(filepath.Join(config, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AI_AGENT_DEV_ASSETS_DIR", checkout)

	root, err := PrepareGenericRoot(t.TempDir(), "/ws", func() (string, error) { return fakeBinary(t, "bin"), nil })
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(root, configDirName, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != "FROM custom" {
		t.Fatalf("staged Dockerfile = %q, want the trusted checkout override", staged)
	}
}

func TestPrepareGenericRootFailsWhenBinaryIsUnreadable(t *testing.T) {
	if _, err := PrepareGenericRoot(t.TempDir(), "/ws", func() (string, error) { return filepath.Join(t.TempDir(), "absent"), nil }); err == nil {
		t.Fatal("preparing a context without the ai-agent binary must fail closed")
	}
}
