package configstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/securefile"
	"golang.org/x/sys/unix"
)

const (
	journalName    = ".governance-transaction.json"
	lockName       = ".governance-transaction.lock"
	maxJournalSize = 4 << 20
)

type journalEntry struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

type journal struct {
	Entries []journalEntry `json:"entries"`
}

type Snapshot struct {
	Identities      *identity.IdentitiesFile
	Policy          *policy.PolicyFile
	IdentitiesError error
	PolicyError     error
}

type publisher struct {
	write  func(string, []byte) error
	remove func(string) error
}

func Publish(identitiesPath string, identities *identity.IdentitiesFile, policyPath string, policyFile *policy.PolicyFile) error {
	identitiesPath, err := filepath.Abs(identitiesPath)
	if err != nil {
		return fmt.Errorf("resolve identities path: %w", err)
	}
	policyPath, err = filepath.Abs(policyPath)
	if err != nil {
		return fmt.Errorf("resolve policy path: %w", err)
	}
	identitiesData, err := marshal(identities)
	if err != nil {
		return fmt.Errorf("encode identities: %w", err)
	}
	policyData, err := marshal(policyFile)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	p := publisher{write: securefile.WriteOwnerOnly, remove: securefile.Remove}
	return withLock(identitiesPath, func() error {
		if err := p.recover(filepath.Dir(identitiesPath)); err != nil {
			return err
		}
		return p.publish(filepath.Dir(identitiesPath), []journalEntry{{Path: filepath.Clean(identitiesPath), Data: identitiesData}, {Path: filepath.Clean(policyPath), Data: policyData}})
	})
}

func Recover(identitiesPath string) error {
	p := publisher{write: securefile.WriteOwnerOnly, remove: securefile.Remove}
	return withLock(identitiesPath, func() error {
		return p.recover(filepath.Dir(identitiesPath))
	})
}

func Load(identitiesPath, policyPath string) (*identity.IdentitiesFile, *policy.PolicyFile, error) {
	snapshot, err := Inspect(identitiesPath, policyPath)
	if err != nil {
		return nil, nil, err
	}
	if snapshot.IdentitiesError != nil {
		return nil, nil, snapshot.IdentitiesError
	}
	if snapshot.PolicyError != nil {
		return nil, nil, snapshot.PolicyError
	}
	return snapshot.Identities, snapshot.Policy, nil
}

func Inspect(identitiesPath, policyPath string) (Snapshot, error) {
	var snapshot Snapshot
	err := withLock(identitiesPath, func() error {
		p := publisher{write: securefile.WriteOwnerOnly, remove: securefile.Remove}
		if err := p.recover(filepath.Dir(identitiesPath)); err != nil {
			return err
		}
		snapshot.Identities, snapshot.IdentitiesError = identity.Load(identitiesPath)
		snapshot.Policy, snapshot.PolicyError = policy.Load(policyPath)
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func LoadIdentities(identitiesPath string) (*identity.IdentitiesFile, error) {
	var identities *identity.IdentitiesFile
	err := withLock(identitiesPath, func() error {
		p := publisher{write: securefile.WriteOwnerOnly, remove: securefile.Remove}
		if err := p.recover(filepath.Dir(identitiesPath)); err != nil {
			return err
		}
		loaded, err := identity.Load(identitiesPath)
		identities = loaded
		return err
	})
	return identities, err
}

func LoadPolicy(identitiesPath, policyPath string) (*policy.PolicyFile, error) {
	var policyFile *policy.PolicyFile
	err := withLock(identitiesPath, func() error {
		p := publisher{write: securefile.WriteOwnerOnly, remove: securefile.Remove}
		if err := p.recover(filepath.Dir(identitiesPath)); err != nil {
			return err
		}
		loaded, err := policy.Load(policyPath)
		policyFile = loaded
		return err
	})
	return policyFile, err
}

func marshal(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (p publisher) publish(directory string, entries []journalEntry) error {
	transaction := journal{Entries: entries}
	if err := validate(transaction); err != nil {
		return err
	}
	data, err := json.Marshal(transaction)
	if err != nil {
		return fmt.Errorf("encode governance transaction: %w", err)
	}
	if len(data) > maxJournalSize {
		return fmt.Errorf("governance transaction exceeds %d bytes", maxJournalSize)
	}
	for _, entry := range entries {
		if err := os.MkdirAll(filepath.Dir(entry.Path), 0o700); err != nil {
			return fmt.Errorf("create governance directory: %w", err)
		}
	}
	journalPath := filepath.Join(directory, journalName)
	if err := p.write(journalPath, data); err != nil {
		return fmt.Errorf("publish governance transaction: %w", err)
	}
	if err := p.apply(transaction); err != nil {
		return fmt.Errorf("apply committed governance transaction: %w", err)
	}
	if err := p.remove(journalPath); err != nil {
		return fmt.Errorf("complete governance transaction: %w", err)
	}
	return nil
}

func (p publisher) recover(directory string) error {
	journalPath := filepath.Join(directory, journalName)
	data, err := securefile.ReadOwnerOnly(journalPath, maxJournalSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read governance transaction: %w", err)
	}
	var transaction journal
	if err := json.Unmarshal(data, &transaction); err != nil {
		return fmt.Errorf("decode governance transaction: %w", err)
	}
	if err := validate(transaction); err != nil {
		return err
	}
	if err := p.apply(transaction); err != nil {
		return fmt.Errorf("recover governance transaction: %w", err)
	}
	if err := p.remove(journalPath); err != nil {
		return fmt.Errorf("complete governance recovery: %w", err)
	}
	return nil
}

func validate(transaction journal) error {
	if len(transaction.Entries) != 2 {
		return fmt.Errorf("governance transaction must contain two entries")
	}
	seen := make(map[string]struct{}, len(transaction.Entries))
	for _, entry := range transaction.Entries {
		if !filepath.IsAbs(entry.Path) || len(entry.Data) == 0 || len(entry.Data) > 1<<20 {
			return fmt.Errorf("invalid governance transaction entry")
		}
		path := filepath.Clean(entry.Path)
		if _, exists := seen[path]; exists {
			return fmt.Errorf("duplicate governance transaction path")
		}
		seen[path] = struct{}{}
	}
	return nil
}

func (p publisher) apply(transaction journal) error {
	for _, entry := range transaction.Entries {
		if err := p.write(entry.Path, entry.Data); err != nil {
			return err
		}
	}
	return nil
}

func withLock(identitiesPath string, action func() error) error {
	directory := filepath.Dir(identitiesPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create governance directory: %w", err)
	}
	lockPath := filepath.Join(directory, lockName)
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open governance lock: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect governance lock: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("governance lock must be an owner-only regular file")
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock governance configuration: %w", err)
	}
	defer func() { _ = unix.Flock(fd, unix.LOCK_UN) }()
	return action()
}
