package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/adaptive"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

const (
	SchemaVersion = "1"

	StatusOpen      = "open"
	StatusAccepted  = "accepted"
	StatusDismissed = "dismissed"

	MaxEntries     = 500
	maxLedgerBytes = 4 << 20
)

var ErrAmbiguousFingerprint = errors.New("fingerprint prefix matches more than one finding")

type Snapshot struct {
	MatchedRuns         int   `json:"matched_runs,omitempty"`
	TotalTokens         int64 `json:"total_tokens,omitempty"`
	ExtraAgentAttempts  int   `json:"extra_agent_attempts,omitempty"`
	ExtraVerifyAttempts int   `json:"extra_verify_attempts,omitempty"`
	UnverifiedRuns      int   `json:"unverified_runs,omitempty"`
	VerificationPercent int   `json:"verification_percent,omitempty"`
	MissingUsageRuns    int   `json:"missing_usage_runs,omitempty"`
}

type Entry struct {
	Fingerprint      string    `json:"fingerprint"`
	Kind             string    `json:"kind"`
	Repository       string    `json:"repository"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	FirstSeen        time.Time `json:"first_seen"`
	LastSeen         time.Time `json:"last_seen"`
	StatusChangedAt  time.Time `json:"status_changed_at"`
	AcceptedSnapshot *Snapshot `json:"accepted_snapshot,omitempty"`
}

type File struct {
	SchemaVersion string  `json:"schema_version"`
	Entries       []Entry `json:"entries"`
	PrunedEntries int     `json:"pruned_entries,omitempty"`
}

func Fingerprint(kind, repository string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + repository))
	return hex.EncodeToString(digest[:8])
}

func SnapshotOf(finding adaptive.Finding) Snapshot {
	snapshot := Snapshot{
		MatchedRuns:         finding.Evidence.MatchedRuns,
		ExtraAgentAttempts:  finding.Evidence.ExtraAgentAttempts,
		ExtraVerifyAttempts: finding.Evidence.ExtraVerifyAttempts,
		UnverifiedRuns:      finding.Evidence.UnverifiedRuns,
		VerificationPercent: finding.Evidence.VerificationPercent,
		MissingUsageRuns:    finding.Evidence.MissingUsageRuns,
	}
	if finding.Evidence.TotalTokens != nil {
		snapshot.TotalTokens = *finding.Evidence.TotalTokens
	}
	return snapshot
}

func Load(path string) (*File, error) {
	data, err := securefile.ReadOwnerOnly(path, maxLedgerBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &File{SchemaVersion: SchemaVersion}, nil
		}
		return nil, fmt.Errorf("read findings ledger: %w", err)
	}
	file := &File{}
	if err := json.Unmarshal(data, file); err != nil {
		return nil, fmt.Errorf("parse findings ledger %s: %w", path, err)
	}
	if file.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("findings ledger %s has schema %q; this build supports %q", path, file.SchemaVersion, SchemaVersion)
	}
	return file, nil
}

func (f *File) Save(path string) error {
	f.SchemaVersion = SchemaVersion
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode findings ledger: %w", err)
	}
	if err := securefile.WriteOwnerOnly(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write findings ledger: %w", err)
	}
	return nil
}

func (f *File) Sync(findings []adaptive.Finding, now time.Time) {
	byFingerprint := make(map[string]int, len(f.Entries))
	for i, entry := range f.Entries {
		byFingerprint[entry.Fingerprint] = i
	}
	for _, finding := range findings {
		fingerprint := Fingerprint(finding.Kind, finding.Repository)
		if i, seen := byFingerprint[fingerprint]; seen {
			f.Entries[i].LastSeen = now
			f.Entries[i].Title = finding.Title
			continue
		}
		f.Entries = append(f.Entries, Entry{
			Fingerprint:     fingerprint,
			Kind:            finding.Kind,
			Repository:      finding.Repository,
			Title:           finding.Title,
			Status:          StatusOpen,
			FirstSeen:       now,
			LastSeen:        now,
			StatusChangedAt: now,
		})
		byFingerprint[fingerprint] = len(f.Entries) - 1
	}
	f.prune()
}

func (f *File) prune() {
	if len(f.Entries) <= MaxEntries {
		return
	}
	sort.SliceStable(f.Entries, func(i, j int) bool {
		return f.Entries[i].LastSeen.After(f.Entries[j].LastSeen)
	})
	kept := make([]Entry, 0, MaxEntries)
	dropped := 0
	for _, entry := range f.Entries {
		if len(kept) < MaxEntries || entry.Status == StatusAccepted {
			kept = append(kept, entry)
			continue
		}
		dropped++
	}
	f.Entries = kept
	f.PrunedEntries += dropped
}

func (f *File) Find(fingerprintPrefix string) (*Entry, error) {
	prefix := strings.TrimSpace(fingerprintPrefix)
	if prefix == "" {
		return nil, fmt.Errorf("fingerprint is required")
	}
	var match *Entry
	for i := range f.Entries {
		if !strings.HasPrefix(f.Entries[i].Fingerprint, prefix) {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("%w: %q", ErrAmbiguousFingerprint, prefix)
		}
		match = &f.Entries[i]
	}
	if match == nil {
		return nil, fmt.Errorf("no finding matches fingerprint %q", prefix)
	}
	return match, nil
}

func (f *File) SetStatus(fingerprintPrefix, status string, snapshot *Snapshot, now time.Time) (Entry, error) {
	switch status {
	case StatusOpen, StatusAccepted, StatusDismissed:
	default:
		return Entry{}, fmt.Errorf("invalid finding status %q", status)
	}
	entry, err := f.Find(fingerprintPrefix)
	if err != nil {
		return Entry{}, err
	}
	entry.Status = status
	entry.StatusChangedAt = now
	if status == StatusAccepted {
		entry.AcceptedSnapshot = snapshot
	} else {
		entry.AcceptedSnapshot = nil
	}
	return *entry, nil
}
