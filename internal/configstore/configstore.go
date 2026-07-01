package configstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"golang.org/x/sys/unix"
)

const (
	journalName    = ".governance-transaction.json"
	lockName       = ".governance-transaction.lock"
	maxFileSize    = 1 << 20
	maxJournalSize = 4 << 20
)

type storePaths struct {
	identities string
	policy     string
	directory  string
}

type transaction struct {
	IdentitiesPath string `json:"identities_path"`
	PolicyPath     string `json:"policy_path"`
	Identities     []byte `json:"identities"`
	Policy         []byte `json:"policy"`
}

type Snapshot struct {
	Identities      *identity.IdentitiesFile
	Policy          *policy.PolicyFile
	IdentitiesError error
	PolicyError     error
}

func Publish(identitiesPath string, identities *identity.IdentitiesFile, policyPath string, policyFile *policy.PolicyFile) error {
	paths, err := resolve(identitiesPath, policyPath)
	if err != nil {
		return err
	}
	identitiesData, err := marshal(identities)
	if err != nil {
		return fmt.Errorf("encode identities: %w", err)
	}
	policyData, err := marshal(policyFile)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	next := transaction{IdentitiesPath: paths.identities, PolicyPath: paths.policy, Identities: identitiesData, Policy: policyData}
	return locked(paths, func() error {
		if err := recoverTransaction(paths); err != nil {
			return err
		}
		return publish(paths, next)
	})
}

func Load(identitiesPath, policyPath string) (Snapshot, error) {
	paths, err := resolve(identitiesPath, policyPath)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	err = locked(paths, func() error {
		if err := recoverTransaction(paths); err != nil {
			return err
		}
		snapshot.Identities, snapshot.IdentitiesError = identity.Load(paths.identities)
		snapshot.Policy, snapshot.PolicyError = policy.Load(paths.policy)
		return nil
	})
	return snapshot, err
}

func resolve(identitiesPath, policyPath string) (storePaths, error) {
	identitiesPath, err := filepath.Abs(identitiesPath)
	if err != nil {
		return storePaths{}, fmt.Errorf("resolve identities path: %w", err)
	}
	policyPath, err = filepath.Abs(policyPath)
	if err != nil {
		return storePaths{}, fmt.Errorf("resolve policy path: %w", err)
	}
	return storePaths{identities: filepath.Clean(identitiesPath), policy: filepath.Clean(policyPath), directory: filepath.Dir(identitiesPath)}, nil
}

func marshal(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func publish(paths storePaths, next transaction) error {
	if err := validate(next, paths); err != nil {
		return err
	}
	data, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode governance transaction: %w", err)
	}
	if len(data) > maxJournalSize {
		return fmt.Errorf("governance transaction exceeds %d bytes", maxJournalSize)
	}
	for _, directory := range []string{paths.directory, filepath.Dir(paths.policy)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create governance directory: %w", err)
		}
	}
	journalPath := filepath.Join(paths.directory, journalName)
	if err := securefile.WriteOwnerOnly(journalPath, data); err != nil {
		return fmt.Errorf("publish governance transaction: %w", err)
	}
	if err := apply(paths, next); err != nil {
		return fmt.Errorf("apply committed governance transaction: %w", err)
	}
	if err := securefile.Remove(journalPath); err != nil {
		return fmt.Errorf("complete governance transaction: %w", err)
	}
	return nil
}

func recoverTransaction(paths storePaths) error {
	journalPath := filepath.Join(paths.directory, journalName)
	data, err := securefile.ReadOwnerOnly(journalPath, maxJournalSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read governance transaction: %w", err)
	}
	var pending transaction
	if err := json.Unmarshal(data, &pending); err != nil {
		return fmt.Errorf("decode governance transaction: %w", err)
	}
	if err := validate(pending, paths); err != nil {
		return err
	}
	if err := apply(paths, pending); err != nil {
		return fmt.Errorf("recover governance transaction: %w", err)
	}
	if err := securefile.Remove(journalPath); err != nil {
		return fmt.Errorf("complete governance recovery: %w", err)
	}
	return nil
}

func validate(pending transaction, paths storePaths) error {
	if pending.IdentitiesPath != paths.identities || pending.PolicyPath != paths.policy {
		return fmt.Errorf("governance transaction targets do not match configured paths")
	}
	if len(pending.Identities) == 0 || len(pending.Identities) > maxFileSize || len(pending.Policy) == 0 || len(pending.Policy) > maxFileSize {
		return fmt.Errorf("invalid governance transaction payload")
	}
	return nil
}

func apply(paths storePaths, pending transaction) error {
	if err := securefile.WriteOwnerOnly(paths.identities, pending.Identities); err != nil {
		return err
	}
	return securefile.WriteOwnerOnly(paths.policy, pending.Policy)
}

func locked(paths storePaths, action func() error) error {
	if err := os.MkdirAll(paths.directory, 0o700); err != nil {
		return fmt.Errorf("create governance directory: %w", err)
	}
	lockPath := filepath.Join(paths.directory, lockName)
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
