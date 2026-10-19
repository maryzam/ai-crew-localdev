package telemetry

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

func TestEvidenceRootIsCreatedOwnerOnlyAndUnsafeRootsAreRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence")
	if _, err := evidence.OpenRoot(path); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{path, filepath.Join(path, evidence.RunsDir), filepath.Join(path, evidence.CollectorDir)} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %v, want 0700", dir, info.Mode().Perm())
		}
	}

	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.OpenRoot(shared); err == nil {
		t.Error("a group-readable evidence root was accepted")
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.OpenRoot(link); err == nil {
		t.Error("a symlinked evidence root was accepted")
	}

	if _, err := evidence.OpenRoot("relative/evidence"); err == nil {
		t.Error("a relative evidence root was accepted")
	}
}

func TestEvidenceVolumeBelowTheFreeSpaceFloorIsRefused(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	if err := root.RequireFreeSpace(1); err != nil {
		t.Fatalf("a one-byte floor was refused: %v", err)
	}
	if err := root.RequireFreeSpace(math.MaxUint64); err == nil {
		t.Fatal("an unreachable floor was accepted")
	}
}

func writeRunFile(t *testing.T, root *evidence.Root, run, name string, size int, modified time.Time) string {
	t.Helper()
	dir, err := root.RunDir(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTelemetryBudgetPrunesOldestInactiveRunsAndRecordsEachDeletion(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	activeOldest := writeRunFile(t, root, "run-active", evidence.TelemetryFile, 400, base)
	oldest := writeRunFile(t, root, "run-a", evidence.TelemetryFile, 300, base.Add(time.Minute))
	middle := writeRunFile(t, root, "run-b", evidence.TelemetryFile, 300, base.Add(2*time.Minute))
	newest := writeRunFile(t, root, "run-c", evidence.TelemetryFile, 300, base.Add(3*time.Minute))
	audit := writeRunFile(t, root, "run-a", evidence.AuditFile, 10_000, base)
	lifecycle := writeRunFile(t, root, "run-a", evidence.RunFile, 10_000, base)

	pruned, err := root.PruneTelemetry(800, map[string]bool{"run-active": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oldest, middle} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived pruning", path)
		}
	}
	for _, path := range []string{activeOldest, newest, audit, lifecycle} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was pruned: %v", path, err)
		}
	}
	if len(pruned) != 2 || pruned[0].Run != "run-a" || pruned[1].Run != "run-b" {
		t.Fatalf("pruned %+v, want run-a then run-b", pruned)
	}

	f, err := os.Open(root.PruningPath())
	if err != nil {
		t.Fatalf("pruning was not recorded: %v", err)
	}
	defer func() { _ = f.Close() }()
	var recorded []evidence.PrunedTelemetry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var p evidence.PrunedTelemetry
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		recorded = append(recorded, p)
	}
	if len(recorded) != 2 || recorded[0].Run != "run-a" || recorded[0].Bytes != 300 || recorded[1].Run != "run-b" {
		t.Fatalf("pruning record %+v", recorded)
	}

	if again, err := root.PruneTelemetry(800, map[string]bool{"run-active": true}); err != nil || len(again) != 0 {
		t.Fatalf("pruning within budget deleted %+v (err %v)", again, err)
	}
}
