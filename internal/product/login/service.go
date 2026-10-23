package login

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

const (
	podmanHealthBudget = 10 * time.Second
	imageBuildBudget   = 10 * time.Minute
	loginBudget        = 15 * time.Minute
	probeBudget        = 2 * time.Minute
	refreshCopyBudget  = 2 * time.Minute
	cleanupBudget      = 10 * time.Second
	terminationGrace   = 10 * time.Second
	claudeOutputLimit  = 64 << 10
)

type Console struct {
	In       io.Reader
	Out      io.Writer
	Err      io.Writer
	Terminal bool
}

type Service struct {
	registry agents.Registry
	console  Console
	now      func() time.Time
	arch     string
}

func New(registry agents.Registry, console Console) *Service {
	return &Service{registry: registry, console: console, now: time.Now, arch: runtime.GOARCH}
}

func (s *Service) Login(ctx context.Context, name string) (Credential, error) {
	agent, ok := s.registry.Find(name)
	if !ok {
		return Credential{}, fmt.Errorf("unsupported agent %q; choose %s", name, strings.Join(s.registry.Names(), " or "))
	}
	if !s.console.Terminal {
		return Credential{}, fmt.Errorf("ai login %s needs an interactive terminal for the native login", agent.Name)
	}
	paths, err := ResolvePaths(agent)
	if err != nil {
		return Credential{}, err
	}
	if err := preparePaths(paths); err != nil {
		return Credential{}, err
	}
	journal, err := evidence.Open(paths.Evidence)
	if err != nil {
		return Credential{}, err
	}
	lock, err := securefile.LockDirectory(paths.LoginRoot, 0)
	if errors.Is(err, securefile.ErrBusy) {
		return Credential{}, errors.New("another login is already in progress")
	}
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = lock.Close() }()
	started := s.now()
	if err := journal.Append(custodyJournal, custodyRecord{Time: started.UTC(), Agent: agent.Name, Decision: decisionRequested}); err != nil {
		return Credential{}, err
	}
	result, err := s.stage(ctx, agent, paths)
	if err == nil {
		credential, publishErr := s.publish(journal, agent, paths, result, started)
		if publishErr == nil || credential.Generation != "" {
			return credential, publishErr
		}
		err = publishErr
	}
	failure := custodyRecord{Time: s.now().UTC(), Agent: agent.Name, Decision: decisionFailed, Reason: stageOf(err), Image: result.image.id, BinaryHash: result.binaryHash, DurationMS: s.now().Sub(started).Milliseconds()}
	return Credential{}, errors.Join(err, journal.Append(custodyJournal, failure))
}

func stageOf(err error) string {
	var failed stageError
	if errors.As(err, &failed) {
		return failed.stage
	}
	return "internal"
}
