package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/adaptive"
)

func testFinding(kind, repository, title string) adaptive.Finding {
	tokens := int64(120000)
	return adaptive.Finding{
		Kind:       kind,
		Repository: repository,
		Title:      title,
		Evidence: adaptive.Evidence{
			MatchedRuns:         4,
			TotalTokens:         &tokens,
			ExtraVerifyAttempts: 6,
		},
	}
}

func TestFingerprintIsStableAndScopeSensitive(t *testing.T) {
	base := testFinding("retry_waste", "owner/repo", "title")
	a := Fingerprint(base)
	if a != Fingerprint(base) {
		t.Fatal("fingerprint is not stable")
	}
	if a == Fingerprint(testFinding("retry_waste", "owner/other", "title")) {
		t.Fatal("fingerprint must be sensitive to repository")
	}
	if a == Fingerprint(testFinding("high_token_run", "owner/repo", "title")) {
		t.Fatal("fingerprint must be sensitive to kind")
	}
	if len(a) != 16 {
		t.Fatalf("fingerprint length = %d, want 16", len(a))
	}
}

func TestSyncTracksNewAndSeenFindings(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	file := &File{SchemaVersion: SchemaVersion}

	file.Sync([]adaptive.Finding{testFinding("retry_waste", "owner/repo", "first title")}, now)
	if len(file.Entries) != 1 || file.Entries[0].Status != StatusOpen || !file.Entries[0].FirstSeen.Equal(now) {
		t.Fatalf("entries = %+v", file.Entries)
	}

	later := now.Add(24 * time.Hour)
	file.Sync([]adaptive.Finding{testFinding("retry_waste", "owner/repo", "updated title")}, later)
	if len(file.Entries) != 1 {
		t.Fatalf("re-seen finding duplicated: %+v", file.Entries)
	}
	if !file.Entries[0].LastSeen.Equal(later) || !file.Entries[0].FirstSeen.Equal(now) {
		t.Fatalf("timestamps wrong: %+v", file.Entries[0])
	}
	if file.Entries[0].Title != "updated title" {
		t.Fatalf("title not refreshed: %q", file.Entries[0].Title)
	}
}

func TestStatusTransitionsAndSnapshots(t *testing.T) {
	now := time.Now().UTC()
	file := &File{SchemaVersion: SchemaVersion}
	file.Sync([]adaptive.Finding{testFinding("retry_waste", "owner/repo", "title")}, now)
	fingerprint := file.Entries[0].Fingerprint

	snapshot := SnapshotOf(testFinding("retry_waste", "owner/repo", "title"))
	entry, err := file.SetStatus(fingerprint[:6], StatusAccepted, &snapshot, now)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if entry.Status != StatusAccepted || entry.AcceptedSnapshot == nil || entry.AcceptedSnapshot.ExtraVerifyAttempts != 6 {
		t.Fatalf("accepted entry = %+v", entry)
	}

	entry, err = file.SetStatus(fingerprint, StatusDismissed, nil, now)
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if entry.Status != StatusDismissed || entry.AcceptedSnapshot != nil {
		t.Fatalf("dismissed entry keeps snapshot: %+v", entry)
	}

	if _, err := file.SetStatus(fingerprint, "resolved", nil, now); err == nil {
		t.Fatal("invalid status accepted")
	}
	if _, err := file.SetStatus("ffff", StatusOpen, nil, now); err == nil {
		t.Fatal("unknown fingerprint accepted")
	}
}

func TestFindRejectsAmbiguousPrefixes(t *testing.T) {
	file := &File{SchemaVersion: SchemaVersion, Entries: []Entry{
		{Fingerprint: "aa11"}, {Fingerprint: "aa22"},
	}}
	if _, err := file.Find("aa"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous prefix not rejected: %v", err)
	}
	if _, err := file.Find("aa1"); err != nil {
		t.Fatalf("unique prefix rejected: %v", err)
	}
}

func TestLoadSaveRoundTripAndSchemaGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")

	empty, err := Load(path)
	if err != nil || len(empty.Entries) != 0 {
		t.Fatalf("missing ledger must load empty: %+v, %v", empty, err)
	}

	now := time.Now().UTC()
	empty.Sync([]adaptive.Finding{testFinding("retry_waste", "owner/repo", "title")}, now)
	if err := empty.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil || len(loaded.Entries) != 1 || loaded.Entries[0].Kind != "retry_waste" {
		t.Fatalf("roundtrip: %+v, %v", loaded, err)
	}

	if err := os.WriteFile(path, []byte(`{"schema_version":"999"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("unknown schema not refused: %v", err)
	}

	if err := os.WriteFile(path, []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("corrupt ledger must fail closed, not be clobbered")
	}
}

func TestSaveCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "nested", "ledger.json")
	file := &File{SchemaVersion: SchemaVersion}
	file.Sync([]adaptive.Finding{testFinding("retry_waste", "owner/repo", "title")}, time.Now().UTC())
	if err := file.Save(path); err != nil {
		t.Fatalf("save into missing directory: %v", err)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.Entries) != 1 {
		t.Fatalf("reload after save into missing directory: %+v, %v", loaded, err)
	}
}

func TestMutateSerializesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "ledger.json")
	now := time.Now().UTC()
	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			finding := testFinding("repeated_failure", fmt.Sprintf("owner/repo-%d", i), "title")
			_, err := Mutate(path, func(f *File) error {
				f.Sync([]adaptive.Finding{finding}, now)
				return nil
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent mutate: %v", err)
		}
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Entries) != writers {
		t.Fatalf("lost updates under concurrency: %d entries, want %d", len(loaded.Entries), writers)
	}
}

func TestPruneEnforcesHardBudgetForAcceptedEntries(t *testing.T) {
	now := time.Now().UTC()
	file := &File{SchemaVersion: SchemaVersion}
	for i := 0; i < MaxEntries+50; i++ {
		file.Entries = append(file.Entries, Entry{
			Fingerprint: fmt.Sprintf("%016x", i),
			Status:      StatusAccepted,
			LastSeen:    now.Add(-time.Duration(i) * time.Hour),
		})
	}
	file.prune()
	if len(file.Entries) != MaxEntries {
		t.Fatalf("hard budget not enforced for accepted entries: %d entries", len(file.Entries))
	}
	if file.PrunedEntries != 50 {
		t.Fatalf("pruned count = %d, want 50", file.PrunedEntries)
	}
}

func TestPruneKeepsAcceptedAndNewestWithinBudget(t *testing.T) {
	now := time.Now().UTC()
	file := &File{SchemaVersion: SchemaVersion}
	for i := 0; i < MaxEntries+10; i++ {
		file.Entries = append(file.Entries, Entry{
			Fingerprint: fmt.Sprintf("%016x", i),
			Status:      StatusOpen,
			LastSeen:    now.Add(-time.Duration(i) * time.Hour),
		})
	}
	file.Entries[MaxEntries+5].Status = StatusAccepted
	oldestAccepted := file.Entries[MaxEntries+5].Fingerprint

	file.prune()

	if len(file.Entries) > MaxEntries+1 {
		t.Fatalf("entries = %d, want at most budget plus retained accepted", len(file.Entries))
	}
	if file.PrunedEntries == 0 {
		t.Fatal("pruning must be recorded, not silent")
	}
	found := false
	for _, entry := range file.Entries {
		if entry.Fingerprint == oldestAccepted {
			found = true
		}
	}
	if !found {
		t.Fatal("accepted entries must survive pruning")
	}
}
