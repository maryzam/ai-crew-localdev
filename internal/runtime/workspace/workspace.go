package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

const metadataVersion = 2
const transitionEvidenceLimit = 128
const repositoryConfigLimit = 16 << 10

const (
	sourceInspectionTimeout = 30 * time.Second
	provisionTimeout        = 5 * time.Minute
	finalizationTimeout     = 2 * time.Minute
	applyTimeout            = 2 * time.Minute
)

var workspaceIDPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)
var sourceKeyPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40,64}$`)
var githubNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type State string

const (
	StateActive      State = "active"
	StateRunning     State = "running"
	StateResultReady State = "result-ready"
	StateApplying    State = "applying"
	StateApplied     State = "applied"
	StateFailed      State = "failed"
)

type RunOutcome string

const (
	RunSucceeded   RunOutcome = "succeeded"
	RunFailed      RunOutcome = "failed"
	RunCanceled    RunOutcome = "canceled"
	RunInterrupted RunOutcome = "interrupted"
)

type TransitionEvidence struct {
	Sequence      uint64     `json:"sequence"`
	At            time.Time  `json:"at"`
	From          State      `json:"from,omitempty"`
	To            State      `json:"to"`
	Reason        string     `json:"reason"`
	Outcome       RunOutcome `json:"outcome,omitempty"`
	ElapsedMillis int64      `json:"elapsed_ms"`
	BudgetMillis  int64      `json:"budget_ms"`
}

type Workspace struct {
	Version            int                  `json:"version"`
	ID                 string               `json:"id"`
	SourceRoot         string               `json:"source_root"`
	Slug               string               `json:"slug"`
	Remote             string               `json:"remote,omitempty"`
	SourceBranch       string               `json:"source_branch"`
	BaseCommit         string               `json:"base_commit"`
	ResultCommit       string               `json:"result_commit,omitempty"`
	Branch             string               `json:"branch"`
	AgentName          string               `json:"agent_name,omitempty"`
	Tool               string               `json:"tool,omitempty"`
	RepositoryConfig   string               `json:"repository_config"`
	PlanDigest         string               `json:"plan_digest"`
	State              State                `json:"state"`
	LeaseID            string               `json:"lease_id,omitempty"`
	LastOutcome        RunOutcome           `json:"last_outcome,omitempty"`
	LastReason         string               `json:"last_reason,omitempty"`
	TransitionsDropped uint64               `json:"transitions_dropped,omitempty"`
	TransitionOrigin   State                `json:"transition_origin,omitempty"`
	Transitions        []TransitionEvidence `json:"transitions"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	CheckoutPath       string               `json:"-"`
	sourceKey          string
	applyRoot          string
}

type PrepareRequest struct {
	SourcePath string
	New        bool
	AgentName  string
	Tool       string
}

type Preparation struct {
	request PrepareRequest
	root    string
	key     string
	source  sourceResolution
	active  Workspace
}

type Author struct {
	Name  string
	Email string
}

type RunLease struct {
	ID          string
	workspaceID string
	sourceKey   string
	fd          int
}

type Stage string

const (
	StagePrepare    Stage = "prepare"
	StageResume     Stage = "resume"
	StageClone      Stage = "clone"
	StageCheckpoint Stage = "checkpoint"
	StageAcquire    Stage = "acquire"
	StageComplete   Stage = "complete"
	StageApply      Stage = "apply"
	StageActive     Stage = "active"
	StageLoad       Stage = "load"
	StagePreflight  Stage = "preflight"
)

type Outcome string

const (
	OutcomeStarted   Outcome = "started"
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

type Event struct {
	Stage       Stage
	Outcome     Outcome
	WorkspaceID string
	Elapsed     time.Duration
	Budget      time.Duration
	Err         error
}

type Observer func(Event)

type Manager struct {
	root     string
	git      gitRunner
	now      func() time.Time
	newID    func() (string, error)
	Observer Observer
}

func NewManager(dataDir string) Manager {
	return Manager{
		root: filepath.Join(dataDir, "workspaces"),
		git:  newGitRunner(),
		now:  time.Now,
		newID: func() (string, error) {
			value := make([]byte, 12)
			if _, err := rand.Read(value); err != nil {
				return "", err
			}
			return hex.EncodeToString(value), nil
		},
	}
}

func (manager Manager) Root() string {
	return manager.root
}

func (manager Manager) Active(ctx context.Context, sourcePath string) (Workspace, error) {
	started := manager.now()
	manager.emit(Event{Stage: StageActive, Outcome: OutcomeStarted, Budget: sourceInspectionTimeout})
	operationCtx, cancel := context.WithTimeout(ctx, sourceInspectionTimeout)
	defer cancel()
	workspace, err := manager.active(operationCtx, sourcePath)
	manager.emitCompletion(StageActive, workspace.ID, started, sourceInspectionTimeout, err)
	return workspace, err
}

func (manager Manager) Preflight(ctx context.Context, request PrepareRequest) error {
	_, err := manager.Inspect(ctx, request)
	return err
}

func (manager Manager) Inspect(ctx context.Context, request PrepareRequest) (Preparation, error) {
	started := manager.now()
	manager.emit(Event{Stage: StagePreflight, Outcome: OutcomeStarted, Budget: sourceInspectionTimeout})
	operationCtx, cancel := context.WithTimeout(ctx, sourceInspectionTimeout)
	defer cancel()
	planned, err := manager.inspect(operationCtx, request)
	manager.emitCompletion(StagePreflight, "", started, sourceInspectionTimeout, err)
	return planned, err
}

func (manager Manager) inspect(ctx context.Context, request PrepareRequest) (Preparation, error) {
	root, err := manager.repositoryRoot(ctx, request.SourcePath)
	if err != nil {
		return Preparation{}, err
	}
	key := sourceKey(root)
	planned := Preparation{request: request, root: root, key: key}
	if !request.New {
		active, found, loadErr := manager.loadActive(manager.sourceDirectory(key), root, key)
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			return Preparation{}, loadErr
		}
		if found && active.State != StateApplied {
			if err := manager.validateSourceIdentity(ctx, root, active); err != nil {
				return Preparation{}, err
			}
			planned.active = active
			return planned, nil
		}
	}
	planned.source, err = manager.resolveSource(ctx, root)
	if err != nil {
		return Preparation{}, err
	}
	return planned, nil
}

func (manager Manager) Load(ctx context.Context, sourcePath, workspaceID string) (Workspace, error) {
	started := manager.now()
	manager.emit(Event{Stage: StageLoad, Outcome: OutcomeStarted, WorkspaceID: workspaceID, Budget: sourceInspectionTimeout})
	operationCtx, cancel := context.WithTimeout(ctx, sourceInspectionTimeout)
	defer cancel()
	loaded, err := manager.load(operationCtx, sourcePath, workspaceID)
	manager.emitCompletion(StageLoad, workspaceID, started, sourceInspectionTimeout, err)
	return loaded, err
}

func (manager Manager) load(ctx context.Context, sourcePath, workspaceID string) (Workspace, error) {
	if err := ensureOwnerDirectory(manager.root); err != nil {
		return Workspace{}, fmt.Errorf("prepare workspace root: %w", err)
	}
	root, err := manager.repositoryRoot(ctx, sourcePath)
	if err != nil {
		return Workspace{}, err
	}
	key := sourceKey(root)
	if _, statErr := os.Lstat(manager.workspaceDirectory(key, workspaceID)); errors.Is(statErr, os.ErrNotExist) {
		located, locateErr := manager.findWorkspace(ctx, workspaceID)
		if locateErr != nil {
			return Workspace{}, fmt.Errorf("load workspace %s: %w", workspaceID, locateErr)
		}
		if identityErr := manager.validateSourceIdentity(ctx, root, located); identityErr != nil {
			return Workspace{}, identityErr
		}
		if located.SourceRoot != root {
			if _, originalErr := os.Lstat(located.SourceRoot); originalErr == nil {
				return Workspace{}, fmt.Errorf("load workspace %s: source remains at %s and does not match %s", workspaceID, located.SourceRoot, root)
			} else if !errors.Is(originalErr, os.ErrNotExist) {
				return Workspace{}, fmt.Errorf("load workspace %s: inspect original source %s: %w", workspaceID, located.SourceRoot, originalErr)
			}
		}
		located.applyRoot = root
		return located, nil
	} else if statErr != nil {
		return Workspace{}, fmt.Errorf("inspect workspace %s: %w", workspaceID, statErr)
	}
	var loaded Workspace
	err = withSourceLock(ctx, manager.sourceDirectory(key), func() error {
		workspace, loadErr := manager.loadWorkspace(key, workspaceID)
		if loadErr != nil {
			return loadErr
		}
		if workspace.SourceRoot != root {
			return fmt.Errorf("workspace source does not match %s", root)
		}
		loaded = workspace
		return nil
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("load workspace %s: %w", workspaceID, err)
	}
	loaded.applyRoot = root
	return loaded, err
}

func (manager Manager) active(ctx context.Context, sourcePath string) (Workspace, error) {
	if err := ensureOwnerDirectory(manager.root); err != nil {
		return Workspace{}, fmt.Errorf("prepare workspace root: %w", err)
	}
	root, err := manager.repositoryRoot(ctx, sourcePath)
	if err != nil {
		return Workspace{}, err
	}
	key := sourceKey(root)
	var active Workspace
	err = withSourceLock(ctx, manager.sourceDirectory(key), func() error {
		loaded, found, loadErr := manager.loadActive(manager.sourceDirectory(key), root, key)
		if loadErr != nil {
			return loadErr
		}
		if !found || loaded.State == StateApplied {
			return fmt.Errorf("no active workspace for %s", root)
		}
		active = loaded
		return nil
	})
	return active, err
}

func (manager Manager) Prepare(ctx context.Context, request PrepareRequest) (Workspace, error) {
	inspectionCtx, cancel := context.WithTimeout(ctx, sourceInspectionTimeout)
	defer cancel()
	planned, err := manager.inspect(inspectionCtx, request)
	if err != nil {
		return Workspace{}, err
	}
	return manager.PrepareInspected(ctx, planned, request.AgentName, request.Tool)
}

func (manager Manager) PrepareInspected(ctx context.Context, planned Preparation, agentName, tool string) (Workspace, error) {
	started := manager.now()
	manager.emit(Event{Stage: StagePrepare, Outcome: OutcomeStarted, Budget: provisionTimeout})
	operationCtx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()
	prepared, err := manager.prepareInspected(operationCtx, planned, agentName, tool)
	manager.emitCompletion(StagePrepare, prepared.ID, started, provisionTimeout, err)
	return prepared, err
}

func (manager Manager) prepareInspected(ctx context.Context, planned Preparation, agentName, tool string) (Workspace, error) {
	if err := ensureOwnerDirectory(manager.root); err != nil {
		return Workspace{}, fmt.Errorf("prepare workspace root: %w", err)
	}
	if planned.root == "" || planned.key != sourceKey(planned.root) || planned.request.SourcePath == "" {
		return Workspace{}, fmt.Errorf("invalid source inspection plan")
	}
	root := planned.root
	key := planned.key
	sourceDirectory := manager.sourceDirectory(key)
	var prepared Workspace
	err := withSourceLock(ctx, sourceDirectory, func() error {
		if !planned.request.New {
			resumeStarted := manager.now()
			manager.emit(Event{Stage: StageResume, Outcome: OutcomeStarted})
			active, found, loadErr := manager.loadActive(sourceDirectory, root, key)
			if loadErr != nil {
				manager.emitCompletion(StageResume, "", resumeStarted, 0, loadErr)
				return loadErr
			}
			if found && active.State != StateApplied {
				if planned.active.ID != "" && planned.active.ID != active.ID {
					return fmt.Errorf("active workspace changed after source inspection; retry start")
				}
				if planned.active.ID == "" && (planned.source.slug != active.Slug || planned.source.base != active.BaseCommit || planned.source.branch != active.SourceBranch) {
					return fmt.Errorf("active workspace changed after source inspection; retry start")
				}
				if agentName != "" && (active.AgentName != agentName || active.Tool != tool) {
					return fmt.Errorf("active workspace %s belongs to agent %s using %s; pass --new to create a workspace for agent %s", active.ID, active.AgentName, active.Tool, agentName)
				}
				manager.emitCompletion(StageResume, active.ID, resumeStarted, 0, nil)
				prepared = active
				return nil
			}
			manager.emitCompletion(StageResume, "", resumeStarted, 0, nil)
		}
		if planned.source.root == "" {
			return fmt.Errorf("source inspection plan has no repository snapshot")
		}
		var resolveErr error
		prepared, resolveErr = manager.create(ctx, sourceDirectory, key, planned.source, agentName, tool)
		return resolveErr
	})
	if err != nil {
		return Workspace{}, err
	}
	return prepared, nil
}

func (manager Manager) Acquire(ctx context.Context, workspace Workspace) (*RunLease, error) {
	started := manager.now()
	manager.emit(Event{Stage: StageAcquire, Outcome: OutcomeStarted, WorkspaceID: workspace.ID, Budget: runLockTimeout})
	lease, err := manager.acquire(ctx, workspace)
	manager.emitCompletion(StageAcquire, workspace.ID, started, runLockTimeout, err)
	return lease, err
}

func (manager Manager) acquire(ctx context.Context, workspace Workspace) (*RunLease, error) {
	started := manager.now()
	loaded, err := manager.Load(ctx, workspace.SourceRoot, workspace.ID)
	if err != nil {
		return nil, err
	}
	fd, err := acquireRunLock(ctx, filepath.Join(manager.workspaceDirectory(loaded.sourceKey, loaded.ID), "run.lock"))
	if err != nil {
		return nil, err
	}
	leaseID, err := manager.newID()
	if err != nil || !workspaceIDPattern.MatchString(leaseID) {
		_ = releaseRunLock(fd)
		return nil, fmt.Errorf("create workspace lease ID")
	}
	lease := &RunLease{ID: leaseID, workspaceID: loaded.ID, sourceKey: loaded.sourceKey, fd: fd}
	err = withSourceLock(ctx, manager.sourceDirectory(loaded.sourceKey), func() error {
		current, loadErr := manager.loadWorkspace(loaded.sourceKey, loaded.ID)
		if loadErr != nil {
			return loadErr
		}
		stale := current.State == StateRunning
		if stale {
			manager.transition(&current, StateFailed, "stale_running_lease", RunInterrupted, runLockTimeout, started)
			current.LeaseID = ""
			current.LastOutcome = RunInterrupted
			current.LastReason = "stale_running_lease"
			if saveErr := manager.saveWorkspace(current); saveErr != nil {
				return fmt.Errorf("record stale workspace lease: %w", saveErr)
			}
		}
		switch current.State {
		case StateActive, StateResultReady, StateFailed:
		case StateApplying:
			return fmt.Errorf("workspace %s has an apply operation in progress", current.ID)
		case StateApplied:
			return fmt.Errorf("workspace %s has already been applied", current.ID)
		default:
			return fmt.Errorf("workspace %s cannot start from state %s", current.ID, current.State)
		}
		reason := "launch_started"
		transitionOutcome := RunOutcome("")
		if stale {
			reason = "stale_running_lease_reconciled"
			transitionOutcome = RunInterrupted
		}
		manager.transition(&current, StateRunning, reason, transitionOutcome, runLockTimeout, started)
		current.LeaseID = lease.ID
		if stale {
			current.LastOutcome = RunInterrupted
			current.LastReason = "stale_running_lease_reconciled"
		} else {
			current.LastOutcome = ""
			current.LastReason = "launch_started"
		}
		return manager.saveWorkspace(current)
	})
	if err != nil {
		_ = releaseRunLock(fd)
		return nil, err
	}
	return lease, nil
}

func (manager Manager) Complete(ctx context.Context, workspace Workspace, lease *RunLease, author Author, outcome RunOutcome) (Workspace, error) {
	started := manager.now()
	manager.emit(Event{Stage: StageComplete, Outcome: OutcomeStarted, WorkspaceID: workspace.ID, Budget: finalizationTimeout})
	operationCtx, cancel := context.WithTimeout(ctx, finalizationTimeout)
	defer cancel()
	completed, err := manager.complete(operationCtx, workspace, lease, author, outcome)
	manager.emitCompletion(StageComplete, workspace.ID, started, finalizationTimeout, err)
	return completed, err
}

func (manager Manager) Abort(ctx context.Context, workspace Workspace, lease *RunLease, outcome RunOutcome) error {
	if lease == nil || !workspaceIDPattern.MatchString(lease.ID) || lease.workspaceID != workspace.ID || lease.sourceKey != sourceKey(workspace.SourceRoot) || lease.fd < 0 {
		return fmt.Errorf("workspace run lease does not match workspace %s", workspace.ID)
	}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sourceLockTimeout)
	markErr := manager.markRunFailed(recoveryCtx, workspace, lease, outcome, "container_not_quiesced")
	cancel()
	releaseErr := releaseRunLock(lease.fd)
	lease.fd = -1
	return errors.Join(markErr, releaseErr)
}

func (manager Manager) complete(ctx context.Context, workspace Workspace, lease *RunLease, author Author, outcome RunOutcome) (Workspace, error) {
	if lease == nil || !workspaceIDPattern.MatchString(lease.ID) || lease.workspaceID != workspace.ID || lease.sourceKey != sourceKey(workspace.SourceRoot) || lease.fd < 0 {
		return Workspace{}, fmt.Errorf("workspace run lease does not match workspace %s", workspace.ID)
	}
	var checkpointed Workspace
	var checkpointErr error
	if !validRunOutcome(outcome) || outcome == RunInterrupted {
		checkpointErr = fmt.Errorf("invalid completed run outcome %q", outcome)
	} else {
		checkpointed, checkpointErr = manager.checkpointRun(ctx, workspace, author, lease.ID, outcome)
	}
	if checkpointErr != nil {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sourceLockTimeout)
		markErr := manager.markRunFailed(recoveryCtx, workspace, lease, outcome, "finalize_failed")
		cancel()
		checkpointErr = errors.Join(checkpointErr, markErr)
	}
	releaseErr := releaseRunLock(lease.fd)
	lease.fd = -1
	if checkpointErr != nil || releaseErr != nil {
		return checkpointed, errors.Join(checkpointErr, releaseErr)
	}
	return checkpointed, nil
}

func (manager Manager) markRunFailed(ctx context.Context, workspace Workspace, lease *RunLease, outcome RunOutcome, reason string) error {
	started := manager.now()
	return withSourceLock(ctx, manager.sourceDirectory(lease.sourceKey), func() error {
		current, err := manager.loadWorkspace(lease.sourceKey, workspace.ID)
		if err != nil {
			return err
		}
		if current.State != StateRunning || current.LeaseID != lease.ID {
			return fmt.Errorf("workspace %s run lease changed during failure recovery", workspace.ID)
		}
		manager.transition(&current, StateFailed, reason, outcome, finalizationTimeout, started)
		current.LeaseID = ""
		current.LastOutcome = outcome
		current.LastReason = reason
		return manager.saveWorkspace(current)
	})
}

func (manager Manager) checkpoint(ctx context.Context, workspace Workspace, author Author) (Workspace, error) {
	started := manager.now()
	manager.emit(Event{Stage: StageCheckpoint, Outcome: OutcomeStarted, WorkspaceID: workspace.ID, Budget: finalizationTimeout})
	operationCtx, cancel := context.WithTimeout(ctx, finalizationTimeout)
	defer cancel()
	checkpointed, err := manager.checkpointRun(operationCtx, workspace, author, "", "")
	manager.emitCompletion(StageCheckpoint, workspace.ID, started, finalizationTimeout, err)
	return checkpointed, err
}

func (manager Manager) checkpointRun(ctx context.Context, workspace Workspace, author Author, leaseID string, outcome RunOutcome) (Workspace, error) {
	started := manager.now()
	if err := ensureOwnerDirectory(manager.root); err != nil {
		return Workspace{}, fmt.Errorf("prepare workspace root: %w", err)
	}
	root, err := manager.repositoryRoot(ctx, workspace.SourceRoot)
	if err != nil {
		return Workspace{}, err
	}
	key := sourceKey(root)
	var checkpointed Workspace
	err = withSourceLock(ctx, manager.sourceDirectory(key), func() error {
		current, loadErr := manager.loadWorkspace(key, workspace.ID)
		if loadErr != nil {
			return loadErr
		}
		if current.SourceRoot != root {
			return fmt.Errorf("workspace source does not match %s", root)
		}
		switch current.State {
		case StateActive, StateResultReady:
			if leaseID != "" {
				return fmt.Errorf("workspace %s is not running under lease %s", current.ID, leaseID)
			}
		case StateRunning:
			if leaseID == "" || current.LeaseID != leaseID {
				return fmt.Errorf("workspace %s run lease does not match", current.ID)
			}
		case StateFailed:
			return fmt.Errorf("workspace %s requires a new run after finalization failed", current.ID)
		case StateApplying:
			return fmt.Errorf("workspace %s has an apply operation in progress", current.ID)
		case StateApplied:
			return fmt.Errorf("workspace %s has already been applied", current.ID)
		default:
			return fmt.Errorf("workspace %s cannot be checkpointed from state %s", current.ID, current.State)
		}
		if secureErr := manager.secureCheckoutConfig(ctx, current); secureErr != nil {
			return secureErr
		}
		status, statusErr := manager.git.run(ctx, current.CheckoutPath, "status", "--porcelain=v1", "--untracked-files=all")
		if statusErr != nil {
			return fmt.Errorf("inspect workspace changes: %w", statusErr)
		}
		if status != "" {
			if strings.TrimSpace(author.Name) == "" || strings.TrimSpace(author.Email) == "" {
				return fmt.Errorf("checkpoint author name and email are required")
			}
			if _, addErr := manager.git.run(ctx, current.CheckoutPath, "add", "-A"); addErr != nil {
				return fmt.Errorf("stage workspace changes: %w", addErr)
			}
			message := "ai-agent workspace " + current.ID + " checkpoint"
			if _, commitErr := manager.git.run(ctx, current.CheckoutPath, "-c", "user.name="+author.Name, "-c", "user.email="+author.Email, "commit", "-q", "-m", message); commitErr != nil {
				return fmt.Errorf("commit workspace checkpoint: %w", commitErr)
			}
		}
		head, headErr := manager.git.run(ctx, current.CheckoutPath, "rev-parse", "--verify", "HEAD^{commit}")
		if headErr != nil {
			return fmt.Errorf("resolve workspace result: %w", headErr)
		}
		descendant, ancestorErr := manager.git.succeeds(ctx, current.CheckoutPath, "merge-base", "--is-ancestor", current.BaseCommit, head)
		if ancestorErr != nil {
			return fmt.Errorf("validate workspace history: %w", ancestorErr)
		}
		if !descendant {
			return fmt.Errorf("workspace result is not descended from base commit %s", current.BaseCommit)
		}
		if _, updateErr := manager.git.run(ctx, current.CheckoutPath, "update-ref", "refs/heads/"+current.Branch, head); updateErr != nil {
			return fmt.Errorf("record workspace result ref: %w", updateErr)
		}
		current.ResultCommit = head
		reason := "checkpoint_ready"
		if outcome != "" {
			reason = "launch_" + string(outcome)
		}
		manager.transition(&current, StateResultReady, reason, outcome, finalizationTimeout, started)
		current.LeaseID = ""
		current.LastOutcome = outcome
		current.LastReason = reason
		if saveErr := manager.saveWorkspace(current); saveErr != nil {
			return saveErr
		}
		checkpointed = current
		return nil
	})
	return checkpointed, err
}

func (manager Manager) Apply(ctx context.Context, workspace Workspace) (Workspace, error) {
	started := manager.now()
	manager.emit(Event{Stage: StageApply, Outcome: OutcomeStarted, WorkspaceID: workspace.ID, Budget: applyTimeout})
	operationCtx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()
	applied, err := manager.apply(operationCtx, workspace)
	manager.emitCompletion(StageApply, workspace.ID, started, applyTimeout, err)
	return applied, err
}

func (manager Manager) apply(ctx context.Context, workspace Workspace) (Workspace, error) {
	started := manager.now()
	if err := ensureOwnerDirectory(manager.root); err != nil {
		return Workspace{}, fmt.Errorf("prepare workspace root: %w", err)
	}
	root := workspace.applyRoot
	if root == "" {
		root = workspace.SourceRoot
	}
	root, err := manager.repositoryRoot(ctx, root)
	if err != nil {
		return Workspace{}, err
	}
	key := workspace.sourceKey
	var applied Workspace
	err = withSourceLock(ctx, manager.sourceDirectory(key), func() error {
		current, loadErr := manager.loadWorkspace(key, workspace.ID)
		if loadErr != nil {
			return loadErr
		}
		if (current.State != StateResultReady && current.State != StateApplying && current.State != StateApplied) || current.ResultCommit == "" {
			return fmt.Errorf("workspace %s has no checkpointed result", current.ID)
		}
		if secureErr := manager.secureCheckoutConfig(ctx, current); secureErr != nil {
			return secureErr
		}
		checkoutStatus, statusErr := manager.git.run(ctx, current.CheckoutPath, "status", "--porcelain=v1", "--untracked-files=all")
		if statusErr != nil {
			return fmt.Errorf("inspect checkpointed workspace: %w", statusErr)
		}
		checkoutHead, headErr := manager.git.run(ctx, current.CheckoutPath, "rev-parse", "--verify", "HEAD^{commit}")
		if headErr != nil {
			return fmt.Errorf("inspect checkpointed result: %w", headErr)
		}
		if checkoutStatus != "" || checkoutHead != current.ResultCommit {
			return fmt.Errorf("workspace %s changed after checkpoint at %s; run ai-agent start %s to review and create a new checkpoint", current.ID, current.CheckoutPath, shellQuoteArgument(root))
		}
		sourceStatus, sourceStatusErr := manager.git.run(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
		if sourceStatusErr != nil {
			return fmt.Errorf("inspect source changes: %w", sourceStatusErr)
		}
		if sourceStatus != "" {
			return dirtySourceError(sourceStatus)
		}
		sourceBranch, sourceBranchErr := manager.git.run(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
		if sourceBranchErr != nil || sourceBranch != current.SourceBranch {
			return fmt.Errorf("source branch changed from %s", current.SourceBranch)
		}
		if operationErr := manager.rejectInProgressOperation(ctx, root); operationErr != nil {
			return operationErr
		}
		if identityErr := manager.validateSourceIdentity(ctx, root, current); identityErr != nil {
			return identityErr
		}
		sourceHead, sourceHeadErr := manager.git.run(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
		if sourceHeadErr != nil {
			return fmt.Errorf("resolve source HEAD: %w", sourceHeadErr)
		}
		if sourceHead == current.ResultCommit && (current.State == StateApplying || current.State == StateApplied) {
			manager.transition(&current, StateApplied, "applied_result_verified", current.LastOutcome, applyTimeout, started)
			current.LastReason = "applied_result_verified"
			if saveErr := manager.saveWorkspace(current); saveErr != nil {
				return saveErr
			}
			if clearErr := manager.clearActive(current); clearErr != nil {
				return clearErr
			}
			applied = current
			return nil
		}
		if sourceHead != current.BaseCommit {
			refspec := "refs/heads/" + current.Branch + ":refs/heads/ai-agent/recovery-" + current.ID
			return fmt.Errorf("source HEAD changed from base %s to %s; the result remains at %s and can be recovered with: git fetch --no-tags %s %s", current.BaseCommit, sourceHead, current.CheckoutPath, shellQuoteArgument(current.CheckoutPath), shellQuoteArgument(refspec))
		}
		if current.ResultCommit != current.BaseCommit {
			filtersConfigured, filtersErr := manager.git.succeeds(ctx, root, "config", "--includes", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
			if filtersErr != nil {
				return fmt.Errorf("inspect source Git filters: %w", filtersErr)
			}
			if filtersConfigured {
				return fmt.Errorf("source repository config declares executable Git filters; apply the result manually")
			}
		}
		if current.State == StateResultReady {
			manager.transition(&current, StateApplying, "apply_started", current.LastOutcome, applyTimeout, started)
			current.LastReason = "apply_started"
			if saveErr := manager.saveWorkspace(current); saveErr != nil {
				return saveErr
			}
		}
		if current.ResultCommit != current.BaseCommit {
			temporaryRef := "refs/ai-agent/workspaces/" + current.ID
			refspec := "refs/heads/" + current.Branch + ":" + temporaryRef
			if _, fetchErr := manager.git.runLocal(ctx, root, "fetch", "--no-tags", current.CheckoutPath, refspec); fetchErr != nil {
				return fmt.Errorf("import workspace result: %w", fetchErr)
			}
			defer func() { _, _ = manager.git.run(context.Background(), root, "update-ref", "-d", temporaryRef) }()
			imported, importedErr := manager.git.run(ctx, root, "rev-parse", "--verify", temporaryRef+"^{commit}")
			if importedErr != nil || imported != current.ResultCommit {
				return fmt.Errorf("imported workspace result does not match checkpoint %s", current.ResultCommit)
			}
			if _, mergeErr := manager.git.run(ctx, root, "merge", "--ff-only", "--no-edit", temporaryRef); mergeErr != nil {
				return fmt.Errorf("fast-forward source to workspace result: %w", mergeErr)
			}
		}
		verifiedHead, verifiedHeadErr := manager.git.run(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
		if verifiedHeadErr != nil || verifiedHead != current.ResultCommit {
			return fmt.Errorf("source HEAD did not reach workspace result %s", current.ResultCommit)
		}
		verifiedStatus, verifiedStatusErr := manager.git.run(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
		if verifiedStatusErr != nil {
			return fmt.Errorf("verify applied source: %w", verifiedStatusErr)
		}
		if verifiedStatus != "" {
			return fmt.Errorf("source repository is not clean after applying workspace result")
		}
		manager.transition(&current, StateApplied, "apply_succeeded", current.LastOutcome, applyTimeout, started)
		current.LastReason = "apply_succeeded"
		if saveErr := manager.saveWorkspace(current); saveErr != nil {
			return saveErr
		}
		if clearErr := manager.clearActive(current); clearErr != nil {
			return clearErr
		}
		applied = current
		return nil
	})
	return applied, err
}

type sourceResolution struct {
	root   string
	slug   string
	remote string
	branch string
	base   string
}

func (manager Manager) repositoryRoot(ctx context.Context, sourcePath string) (string, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return "", fmt.Errorf("source path is required")
	}
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	root, err := manager.git.run(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve repository root for %s: %w", abs, err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("canonicalize repository root %s: %w", root, err)
	}
	return filepath.Clean(canonical), nil
}

func (manager Manager) resolveSource(ctx context.Context, root string) (sourceResolution, error) {
	base, err := manager.git.run(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return sourceResolution{}, fmt.Errorf("source repository must have a committed HEAD: %w", err)
	}
	branch, err := manager.git.run(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return sourceResolution{}, fmt.Errorf("source repository must be on a named branch; HEAD is detached at %s", base)
	}
	if err := manager.rejectInProgressOperation(ctx, root); err != nil {
		return sourceResolution{}, err
	}
	staged, err := manager.git.run(ctx, root, "ls-files", "--stage")
	if err != nil {
		return sourceResolution{}, fmt.Errorf("inspect source index: %w", err)
	}
	for _, line := range strings.Split(staged, "\n") {
		if strings.HasPrefix(line, "160000 ") {
			path := strings.TrimSpace(line)
			if _, candidate, found := strings.Cut(line, "\t"); found {
				path = candidate
			}
			return sourceResolution{}, fmt.Errorf("source repository contains unsupported gitlink %q; remove the submodule or use a repository without gitlinks", path)
		}
	}
	status, err := manager.git.run(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return sourceResolution{}, fmt.Errorf("inspect source repository: %w", err)
	}
	if status != "" {
		return sourceResolution{}, dirtySourceError(status)
	}
	remote, remoteErr := manager.git.run(ctx, root, "remote", "get-url", "origin")
	if remoteErr != nil {
		return sourceResolution{}, fmt.Errorf("source repository must have an origin remote: %w", remoteErr)
	}
	slug, canonicalRemote, err := canonicalGitHubRemote(remote)
	if err != nil {
		return sourceResolution{}, err
	}
	return sourceResolution{root: root, slug: slug, remote: canonicalRemote, branch: branch, base: base}, nil
}

func dirtySourceError(status string) error {
	lines := strings.Split(strings.TrimSpace(status), "\n")
	const displayedPathLimit = 5
	paths := make([]string, 0, min(len(lines), displayedPathLimit))
	for _, line := range lines[:min(len(lines), displayedPathLimit)] {
		path := strings.TrimSpace(line)
		if len(line) > 3 {
			path = line[3:]
		}
		paths = append(paths, fmt.Sprintf("%q", path))
	}
	suffix := ""
	if len(lines) > displayedPathLimit {
		suffix = fmt.Sprintf(" and %d more", len(lines)-displayedPathLimit)
	}
	return fmt.Errorf("source repository has uncommitted changes in %s%s; commit, stash, remove, or gitignore them before retrying", strings.Join(paths, ", "), suffix)
}

func shellQuoteArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (manager Manager) create(ctx context.Context, sourceDirectory, key string, source sourceResolution, agentName, tool string) (Workspace, error) {
	cloneStarted := manager.now()
	manager.emit(Event{Stage: StageClone, Outcome: OutcomeStarted, Budget: provisionTimeout})
	workspace, err := manager.createWorkspace(ctx, sourceDirectory, key, source, agentName, tool)
	manager.emitCompletion(StageClone, workspace.ID, cloneStarted, provisionTimeout, err)
	return workspace, err
}

func (manager Manager) createWorkspace(ctx context.Context, sourceDirectory, key string, source sourceResolution, agentName, tool string) (Workspace, error) {
	started := manager.now()
	if (agentName == "") != (tool == "") || !validIdentityValue(agentName) || !validIdentityValue(tool) {
		return Workspace{}, fmt.Errorf("workspace agent identity and tool must be paired safe values")
	}
	id, err := manager.newID()
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace ID: %w", err)
	}
	if !workspaceIDPattern.MatchString(id) {
		return Workspace{}, fmt.Errorf("generated invalid workspace ID")
	}
	staged, err := os.MkdirTemp(sourceDirectory, ".workspace-*")
	if err != nil {
		return Workspace{}, fmt.Errorf("stage workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(staged) }()
	if err := ensureOwnerDirectory(staged); err != nil {
		return Workspace{}, err
	}
	checkout := filepath.Join(staged, "repo")
	if _, err := manager.git.runLocal(ctx, "", "clone", "--no-hardlinks", "--no-checkout", "--", source.root, checkout); err != nil {
		return Workspace{}, fmt.Errorf("clone source repository: %w", err)
	}
	if err := os.Chmod(checkout, 0o700); err != nil {
		return Workspace{}, fmt.Errorf("secure workspace checkout: %w", err)
	}
	if _, err := manager.git.run(ctx, checkout, "checkout", "--detach", source.base); err != nil {
		return Workspace{}, fmt.Errorf("checkout source base %s: %w", source.base, err)
	}
	branch := "ai-agent/workspace-" + id
	if _, err := manager.git.run(ctx, checkout, "switch", "-c", branch); err != nil {
		return Workspace{}, fmt.Errorf("create workspace branch: %w", err)
	}
	if _, err := manager.git.run(ctx, checkout, "remote", "set-url", "origin", source.remote); err != nil {
		return Workspace{}, fmt.Errorf("configure workspace remote: %w", err)
	}
	repositoryConfig, err := manager.captureRepositoryConfig(ctx, checkout)
	if err != nil {
		return Workspace{}, err
	}
	now := manager.now().UTC()
	workspace := Workspace{
		Version:          metadataVersion,
		ID:               id,
		SourceRoot:       source.root,
		Slug:             source.slug,
		Remote:           source.remote,
		SourceBranch:     source.branch,
		BaseCommit:       source.base,
		Branch:           branch,
		AgentName:        agentName,
		Tool:             tool,
		RepositoryConfig: repositoryConfig,
		PlanDigest:       workspacePlanDigest(source, agentName, tool, repositoryConfig),
		CreatedAt:        now,
		CheckoutPath:     checkout,
		sourceKey:        key,
	}
	manager.transition(&workspace, StateActive, "workspace_prepared", "", provisionTimeout, started)
	workspace.LastReason = "workspace_prepared"
	if err := manager.secureCheckoutConfig(ctx, workspace); err != nil {
		return Workspace{}, fmt.Errorf("validate provisioned workspace: %w", err)
	}
	if err := writeJSON(filepath.Join(staged, "workspace.json"), workspace); err != nil {
		return Workspace{}, fmt.Errorf("write workspace metadata: %w", err)
	}
	finalDirectory := manager.workspaceDirectory(key, id)
	if err := os.Rename(staged, finalDirectory); err != nil {
		return Workspace{}, fmt.Errorf("publish workspace: %w", err)
	}
	if err := securefile.SyncDirectory(sourceDirectory); err != nil {
		return Workspace{}, fmt.Errorf("sync workspace directory: %w", err)
	}
	workspace.CheckoutPath = filepath.Join(finalDirectory, "repo")
	if err := writeJSON(filepath.Join(sourceDirectory, "active.json"), activeWorkspace{ID: id}); err != nil {
		return Workspace{}, fmt.Errorf("publish active workspace: %w", err)
	}
	return workspace, nil
}

func (manager Manager) loadActive(sourceDirectory, sourceRoot, key string) (Workspace, bool, error) {
	var active activeWorkspace
	err := readJSON(filepath.Join(sourceDirectory, "active.json"), &active)
	if errors.Is(err, os.ErrNotExist) {
		return Workspace{}, false, nil
	}
	if err != nil {
		return Workspace{}, false, fmt.Errorf("load active workspace: %w", err)
	}
	workspace, err := manager.loadWorkspace(key, active.ID)
	if err != nil {
		return Workspace{}, false, err
	}
	if workspace.SourceRoot != sourceRoot {
		return Workspace{}, false, fmt.Errorf("active workspace source does not match %s", sourceRoot)
	}
	return workspace, true, nil
}

func (manager Manager) loadWorkspace(key, id string) (Workspace, error) {
	if !workspaceIDPattern.MatchString(id) {
		return Workspace{}, fmt.Errorf("invalid workspace ID")
	}
	directory := manager.workspaceDirectory(key, id)
	if err := ensureOwnerDirectory(directory); err != nil {
		return Workspace{}, err
	}
	var workspace Workspace
	if err := readJSON(filepath.Join(directory, "workspace.json"), &workspace); err != nil {
		return Workspace{}, fmt.Errorf("load workspace metadata: %w", err)
	}
	if workspace.Version != metadataVersion || workspace.ID != id || sourceKey(workspace.SourceRoot) != key || !commitPattern.MatchString(workspace.BaseCommit) {
		return Workspace{}, fmt.Errorf("workspace metadata does not match its managed location")
	}
	if workspace.ResultCommit != "" && !commitPattern.MatchString(workspace.ResultCommit) {
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid result commit")
	}
	if workspace.Branch != "ai-agent/workspace-"+id || !validBranch(workspace.SourceBranch) {
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid branch")
	}
	if workspace.RepositoryConfig == "" || len(workspace.RepositoryConfig) > repositoryConfigLimit || workspace.PlanDigest != workspacePlanDigest(sourceResolution{slug: workspace.Slug, remote: workspace.Remote, branch: workspace.SourceBranch, base: workspace.BaseCommit}, workspace.AgentName, workspace.Tool, workspace.RepositoryConfig) {
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid plan digest")
	}
	if (workspace.AgentName == "") != (workspace.Tool == "") || !validIdentityValue(workspace.AgentName) || !validIdentityValue(workspace.Tool) {
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid agent identity")
	}
	slug, remote, err := canonicalGitHubRemote(workspace.Remote)
	if err != nil || slug != workspace.Slug || remote != workspace.Remote {
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid repository identity")
	}
	switch workspace.State {
	case StateActive:
		if workspace.ResultCommit != "" || workspace.LeaseID != "" {
			return Workspace{}, fmt.Errorf("active workspace metadata must not contain a result commit")
		}
	case StateRunning:
		if !workspaceIDPattern.MatchString(workspace.LeaseID) {
			return Workspace{}, fmt.Errorf("running workspace metadata requires a valid lease")
		}
	case StateResultReady, StateApplying, StateApplied:
		if workspace.ResultCommit == "" || workspace.LeaseID != "" {
			return Workspace{}, fmt.Errorf("workspace metadata state %s requires a result commit", workspace.State)
		}
	case StateFailed:
		if workspace.LeaseID != "" {
			return Workspace{}, fmt.Errorf("failed workspace metadata must not retain a lease")
		}
	default:
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid state")
	}
	if workspace.LastOutcome != "" && !validRunOutcome(workspace.LastOutcome) {
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid run outcome")
	}
	if err := validateTransitionEvidence(workspace); err != nil {
		return Workspace{}, err
	}
	if len(workspace.LastReason) > 128 || strings.ContainsAny(workspace.LastReason, "\x00\r\n") {
		return Workspace{}, fmt.Errorf("workspace metadata has an invalid lifecycle reason")
	}
	workspace.CheckoutPath = filepath.Join(directory, "repo")
	workspace.sourceKey = key
	if info, err := os.Lstat(workspace.CheckoutPath); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Workspace{}, fmt.Errorf("workspace checkout %s is not a directory", workspace.CheckoutPath)
	}
	return workspace, nil
}

func validRunOutcome(outcome RunOutcome) bool {
	switch outcome {
	case RunSucceeded, RunFailed, RunCanceled, RunInterrupted:
		return true
	default:
		return false
	}
}

func validIdentityValue(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func (manager Manager) transition(workspace *Workspace, state State, reason string, outcome RunOutcome, budget time.Duration, started time.Time) {
	now := manager.now().UTC()
	elapsed := now.Sub(started)
	if elapsed < 0 {
		elapsed = 0
	}
	workspace.Transitions = append(workspace.Transitions, TransitionEvidence{
		Sequence:      workspace.TransitionsDropped + uint64(len(workspace.Transitions)) + 1,
		At:            now,
		From:          workspace.State,
		To:            state,
		Reason:        reason,
		Outcome:       outcome,
		ElapsedMillis: elapsed.Milliseconds(),
		BudgetMillis:  budget.Milliseconds(),
	})
	if len(workspace.Transitions) > transitionEvidenceLimit {
		dropped := workspace.Transitions[0]
		workspace.Transitions = append([]TransitionEvidence(nil), workspace.Transitions[1:]...)
		workspace.TransitionsDropped = dropped.Sequence
		workspace.TransitionOrigin = dropped.To
	}
	workspace.State = state
	workspace.UpdatedAt = now
}

func validateTransitionEvidence(workspace Workspace) error {
	if len(workspace.Transitions) == 0 || workspace.Transitions[len(workspace.Transitions)-1].To != workspace.State {
		return fmt.Errorf("workspace metadata has incomplete transition evidence")
	}
	previous := workspace.TransitionOrigin
	if workspace.TransitionsDropped == 0 && previous != "" || workspace.TransitionsDropped > 0 && !validState(previous) {
		return fmt.Errorf("workspace metadata has invalid transition evidence origin")
	}
	for index, transition := range workspace.Transitions {
		if transition.Sequence != workspace.TransitionsDropped+uint64(index)+1 || transition.From != previous || !validState(transition.To) || transition.At.IsZero() || transition.ElapsedMillis < 0 || transition.BudgetMillis <= 0 || !validIdentityValue(transition.Reason) || transition.Reason == "" {
			return fmt.Errorf("workspace metadata has invalid transition evidence")
		}
		if transition.Outcome != "" && !validRunOutcome(transition.Outcome) {
			return fmt.Errorf("workspace metadata has invalid transition outcome")
		}
		previous = transition.To
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StateActive, StateRunning, StateResultReady, StateApplying, StateApplied, StateFailed:
		return true
	default:
		return false
	}
}

func (manager Manager) saveWorkspace(workspace Workspace) error {
	path := filepath.Join(manager.workspaceDirectory(workspace.sourceKey, workspace.ID), "workspace.json")
	if err := writeJSON(path, workspace); err != nil {
		return fmt.Errorf("save workspace metadata: %w", err)
	}
	return nil
}

func (manager Manager) secureCheckoutConfig(ctx context.Context, workspace Workspace) error {
	gitDirectory := filepath.Join(workspace.CheckoutPath, ".git")
	info, err := os.Lstat(gitDirectory)
	if err != nil {
		return fmt.Errorf("inspect workspace git directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("workspace git metadata must be an internal owner-controlled directory")
	}
	if err := os.Chmod(gitDirectory, 0o700); err != nil {
		return fmt.Errorf("secure workspace git directory: %w", err)
	}
	for _, forbidden := range []string{filepath.Join(gitDirectory, "commondir"), filepath.Join(gitDirectory, "objects", "info", "alternates")} {
		if _, err := os.Lstat(forbidden); err == nil {
			return fmt.Errorf("workspace git metadata contains external indirection at %s", forbidden)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect workspace git metadata %s: %w", forbidden, err)
		}
	}
	if err := validateInternalGitTree(ctx, gitDirectory); err != nil {
		return err
	}
	for _, critical := range []string{filepath.Join(gitDirectory, "HEAD"), filepath.Join(gitDirectory, "index"), filepath.Join(gitDirectory, "packed-refs")} {
		info, err := os.Lstat(critical)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect workspace git metadata %s: %w", critical, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Getuid()) || !info.Mode().IsRegular() {
			return fmt.Errorf("workspace git metadata %s must remain an internal owner-controlled file", critical)
		}
	}
	if err := securefile.WriteOwnerOnly(filepath.Join(gitDirectory, "config"), []byte(workspace.RepositoryConfig)); err != nil {
		return fmt.Errorf("replace workspace git configuration: %w", err)
	}
	configured, err := manager.git.run(ctx, workspace.CheckoutPath, "remote", "get-url", "origin")
	if err != nil || configured != workspace.Remote {
		return fmt.Errorf("validate workspace remote configuration")
	}
	return nil
}

func (manager Manager) captureRepositoryConfig(ctx context.Context, checkout string) (string, error) {
	if _, err := manager.git.run(ctx, checkout, "config", "--local", "core.hooksPath", "/dev/null"); err != nil {
		return "", fmt.Errorf("disable workspace hooks: %w", err)
	}
	if _, err := manager.git.run(ctx, checkout, "config", "--local", "core.fsmonitor", "false"); err != nil {
		return "", fmt.Errorf("disable workspace filesystem monitor: %w", err)
	}
	path := filepath.Join(checkout, ".git", "config")
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure workspace git configuration: %w", err)
	}
	data, err := securefile.ReadOwnerOnly(path, repositoryConfigLimit)
	if err != nil {
		return "", fmt.Errorf("capture workspace git configuration: %w", err)
	}
	return string(data), nil
}

func validateInternalGitTree(ctx context.Context, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect workspace git metadata %s: %w", path, walkErr)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("inspect workspace git metadata: %w", ctx.Err())
		default:
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect workspace git metadata %s: %w", path, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("workspace git metadata %s must remain internal and owner-controlled", path)
		}
		return nil
	})
}

func (manager Manager) validateSourceIdentity(ctx context.Context, root string, workspace Workspace) error {
	configured, err := manager.git.run(ctx, root, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("revalidate source repository identity: %w", err)
	}
	slug, remote, err := canonicalGitHubRemote(configured)
	if err != nil {
		return fmt.Errorf("revalidate source repository identity: %w", err)
	}
	if slug != workspace.Slug || remote != workspace.Remote {
		return fmt.Errorf("source repository identity changed from %s at %s to %s at %s", workspace.Slug, workspace.Remote, slug, remote)
	}
	return nil
}

func (manager Manager) clearActive(workspace Workspace) error {
	path := filepath.Join(manager.sourceDirectory(workspace.sourceKey), "active.json")
	var active activeWorkspace
	if err := readJSON(path, &active); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load active workspace: %w", err)
	}
	if active.ID != workspace.ID {
		return nil
	}
	if err := securefile.Remove(path); err != nil {
		return fmt.Errorf("clear active workspace: %w", err)
	}
	return nil
}

func (manager Manager) sourceDirectory(key string) string {
	return filepath.Join(manager.root, key)
}

func (manager Manager) workspaceDirectory(key, id string) string {
	return filepath.Join(manager.sourceDirectory(key), id)
}

func sourceKey(root string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(digest[:16])
}

func workspacePlanDigest(source sourceResolution, agentName, tool, repositoryConfig string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{source.slug, source.remote, source.branch, source.base, agentName, tool, repositoryConfig}, "\n")))
	return hex.EncodeToString(digest[:])
}

func (manager Manager) emit(event Event) {
	if manager.Observer != nil {
		manager.Observer(event)
	}
}

func (manager Manager) emitCompletion(stage Stage, workspaceID string, started time.Time, budget time.Duration, err error) {
	outcome := OutcomeSucceeded
	if err != nil {
		outcome = OutcomeFailed
	}
	manager.emit(Event{Stage: stage, Outcome: outcome, WorkspaceID: workspaceID, Elapsed: manager.now().Sub(started), Budget: budget, Err: err})
}

func (manager Manager) rejectInProgressOperation(ctx context.Context, root string) error {
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "rebase-apply", "rebase-merge", "sequencer"} {
		path, err := manager.git.run(ctx, root, "rev-parse", "--git-path", marker)
		if err != nil {
			return fmt.Errorf("inspect source operation state: %w", err)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("source repository has an in-progress Git operation (%s)", marker)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect source operation state %s: %w", marker, err)
		}
	}
	return nil
}

func canonicalGitHubRemote(remote string) (string, string, error) {
	trimmed := strings.TrimSpace(remote)
	if strings.HasPrefix(trimmed, "git@github.com:") {
		return canonicalGitHubPath(strings.TrimPrefix(trimmed, "git@github.com:"))
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("parse origin remote: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" {
		return "", "", fmt.Errorf("origin remote must use HTTPS or SSH")
	}
	if parsed.Scheme == "https" && parsed.User != nil {
		return "", "", fmt.Errorf("origin remote must not embed credentials")
	}
	if parsed.Scheme == "ssh" && (parsed.User == nil || parsed.User.Username() != "git" || parsed.User.String() != "git" || parsed.Port() != "") {
		return "", "", fmt.Errorf("GitHub SSH origin must use the git user without credentials or a custom port")
	}
	if parsed.Hostname() != "github.com" {
		return "", "", fmt.Errorf("origin remote must use github.com")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("origin remote must not contain a query or fragment")
	}
	return canonicalGitHubPath(strings.TrimPrefix(parsed.EscapedPath(), "/"))
}

func canonicalGitHubPath(repositoryPath string) (string, string, error) {
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")
	parts := strings.Split(repositoryPath, "/")
	if len(parts) != 2 || !githubNamePattern.MatchString(parts[0]) || !githubNamePattern.MatchString(parts[1]) || strings.ContainsAny(repositoryPath, "%\\?#") {
		return "", "", fmt.Errorf("origin remote must identify one GitHub owner and repository")
	}
	slug := parts[0] + "/" + parts[1]
	return slug, "https://github.com/" + slug + ".git", nil
}

func validBranch(branch string) bool {
	return branch != "" && !strings.HasPrefix(branch, "-") && !strings.ContainsAny(branch, "\x00\r\n") && !strings.Contains(branch, "..") && !strings.Contains(branch, "@{")
}
