package evidence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendedRecordsAreDurableAndReadBackInOrder(t *testing.T) {
	root, err := Open(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "second"} {
		if err := root.Append("custody.jsonl", map[string]string{"value": value}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := root.Records("custody.jsonl", 1<<10)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %s, %v", records, err)
	}
	var second map[string]string
	if err := json.Unmarshal(records[1], &second); err != nil || second["value"] != "second" {
		t.Fatalf("second record = %s, %v", records[1], err)
	}
	path, _ := root.Path("custody.jsonl")
	if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode = %v, %v", info, err)
	}
}

func TestMissingJournalHasNoRecords(t *testing.T) {
	root, err := Open(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := root.Records("custody.jsonl", 1<<10)
	if err != nil || len(records) != 0 {
		t.Fatalf("records = %v, %v", records, err)
	}
}

func TestJournalRefusesUnsafeOrDamagedEvidence(t *testing.T) {
	root, err := Open(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Append("../escape.jsonl", "x"); err == nil {
		t.Fatal("journal path escape accepted")
	}
	path, _ := root.Path("custody.jsonl")
	if err := os.WriteFile(path, []byte(`{"value":"torn"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Append("custody.jsonl", "x"); !errors.Is(err, ErrIncompleteRecord) {
		t.Fatalf("append after torn record = %v", err)
	}
	if _, err := root.Records("custody.jsonl", 1<<10); !errors.Is(err, ErrIncompleteRecord) {
		t.Fatalf("read torn record = %v", err)
	}
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Records("custody.jsonl", 1<<10); err == nil {
		t.Fatal("malformed record accepted")
	}
	if _, err := root.Records("custody.jsonl", 4); err == nil {
		t.Fatal("oversized journal accepted")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := root.Append("custody.jsonl", "x"); err == nil {
		t.Fatal("shared journal accepted")
	}
	target := filepath.Join(t.TempDir(), "target.jsonl")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	linked, _ := root.Path("linked.jsonl")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	if err := root.Append("linked.jsonl", "x"); err == nil {
		t.Fatal("symlinked journal accepted")
	}
}
