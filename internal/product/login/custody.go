package login

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

const (
	custodyJournal      = "custody.jsonl"
	custodyJournalLimit = 16 << 20
	decisionRequested   = "login_requested"
	decisionFailed      = "login_failed"
	decisionPublished   = "credential_published"
)

var generationName = regexp.MustCompile(`^[0-9a-f]{16}$`)

type custodyRecord struct {
	Time           time.Time `json:"time"`
	Agent          string    `json:"agent"`
	Decision       string    `json:"decision"`
	Reason         string    `json:"reason,omitempty"`
	Image          string    `json:"image,omitempty"`
	Generation     string    `json:"generation,omitempty"`
	CredentialHash string    `json:"credential_sha256,omitempty"`
	BinaryHash     string    `json:"binary_sha256,omitempty"`
	DurationMS     int64     `json:"duration_ms"`
}

type Credential struct {
	Agent               string
	Generation          string
	Path                string
	SHA256              string
	RefreshBinary       string
	RefreshBinarySHA256 string
}

func Current(registry agents.Registry, name string) (Credential, error) {
	agent, ok := registry.Find(name)
	if !ok {
		return Credential{}, fmt.Errorf("unsupported agent %q", name)
	}
	paths, err := ResolvePaths(agent)
	if err != nil {
		return Credential{}, err
	}
	journal, err := evidence.Open(paths.Evidence)
	if err != nil {
		return Credential{}, err
	}
	record, err := latestPublication(journal, agent.Name)
	if err != nil {
		return Credential{}, err
	}
	return verifyPublication(agent, paths, record, runtime.GOARCH)
}

func latestPublication(journal *evidence.Root, agent string) (custodyRecord, error) {
	records, err := journal.Records(custodyJournal, custodyJournalLimit)
	if err != nil {
		return custodyRecord{}, err
	}
	for index := len(records) - 1; index >= 0; index-- {
		var record custodyRecord
		if err := json.Unmarshal(records[index], &record); err != nil {
			return custodyRecord{}, fmt.Errorf("custody record %d: %w", index+1, err)
		}
		if record.Agent == agent && record.Decision == decisionPublished {
			return record, nil
		}
	}
	return custodyRecord{}, fmt.Errorf("no published %s login; run 'ai login %s'", agent, agent)
}

func verifyPublication(agent agents.Agent, paths Paths, record custodyRecord, arch string) (Credential, error) {
	if !generationName.MatchString(record.Generation) {
		return Credential{}, fmt.Errorf("published %s generation %q is invalid", agent.Name, record.Generation)
	}
	generation := filepath.Join(paths.AgentRoot, record.Generation)
	if err := securefile.RequireOwnerOnlyDir(generation); err != nil {
		return Credential{}, fmt.Errorf("published %s generation: %w", agent.Name, err)
	}
	path := filepath.Join(generation, agent.CredentialFile)
	if err := requireHash(path, agent.CredentialLimit, record.CredentialHash); err != nil {
		return Credential{}, fmt.Errorf("published %s credential is not authoritative: %w", agent.Name, err)
	}
	credential := Credential{Agent: agent.Name, Generation: record.Generation, Path: path, SHA256: record.CredentialHash}
	if agent.Refresh == nil {
		return credential, nil
	}
	pin := agent.Refresh.SHA256[arch]
	if pin == "" || record.BinaryHash != pin {
		return Credential{}, fmt.Errorf("published %s refresh binary does not match the pin for %s", agent.Name, arch)
	}
	if err := requireHash(paths.Binary, agent.Refresh.SizeLimit, pin); err != nil {
		return Credential{}, fmt.Errorf("pinned %s refresh binary is not authoritative: %w", agent.Name, err)
	}
	credential.RefreshBinary = paths.Binary
	credential.RefreshBinarySHA256 = pin
	return credential, nil
}

func requireHash(path string, limit int64, want string) error {
	hash, err := securefile.HashOwnerOnly(path, limit)
	if err != nil {
		return err
	}
	if hash != want {
		return errors.New("content does not match the recorded SHA-256")
	}
	return nil
}

func (s *Service) publish(journal *evidence.Root, agent agents.Agent, paths Paths, result staged, started time.Time) (Credential, error) {
	digest := sha256.Sum256(result.credential)
	record := custodyRecord{Agent: agent.Name, Decision: decisionPublished, Image: result.image.id, CredentialHash: hex.EncodeToString(digest[:]), BinaryHash: result.binaryHash}
	generation, err := writeGeneration(agent, paths, result)
	if err != nil {
		return Credential{}, failAt("publish", err)
	}
	record.Generation = generation
	record.Time = s.now().UTC()
	record.DurationMS = s.now().Sub(started).Milliseconds()
	if err := journal.Append(custodyJournal, record); err != nil {
		return Credential{}, failAt("record", fmt.Errorf("record %s publication; generation %s stays unpublished: %w", agent.Name, generation, err))
	}
	credential, err := verifyPublication(agent, paths, record, s.arch)
	if err != nil {
		return Credential{}, failAt("verify", err)
	}
	if err := pruneGenerations(paths.AgentRoot, generation); err != nil {
		return credential, fmt.Errorf("%s login published as generation %s, but removing superseded generations failed: %w", agent.Name, generation, err)
	}
	return credential, nil
}

func writeGeneration(agent agents.Agent, paths Paths, result staged) (string, error) {
	generation, err := randomHex(8)
	if err != nil {
		return "", fmt.Errorf("create credential generation: %w", err)
	}
	directory := filepath.Join(paths.AgentRoot, generation)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return "", fmt.Errorf("create credential generation: %w", err)
	}
	if err := securefile.WriteOwnerOnly(filepath.Join(directory, agent.CredentialFile), result.credential); err != nil {
		return "", errors.Join(fmt.Errorf("write %s credential: %w", agent.Name, err), os.RemoveAll(directory))
	}
	if err := securefile.SyncDirectory(paths.AgentRoot); err != nil {
		return "", errors.Join(fmt.Errorf("sync %s credential generations: %w", agent.Name, err), os.RemoveAll(directory))
	}
	return generation, nil
}

func pruneGenerations(root, current string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if entry.Name() == current {
			continue
		}
		failures = append(failures, os.RemoveAll(filepath.Join(root, entry.Name())))
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	return securefile.SyncDirectory(root)
}
