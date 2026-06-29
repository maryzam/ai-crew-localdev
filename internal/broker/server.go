package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/correlation"

	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

const (
	connReadTimeout  = 5 * time.Second
	connWriteTimeout = 5 * time.Second
	cleanupInterval  = 5 * time.Minute

	// MaxRequestBytes caps a single broker request body to bound memory.
	MaxRequestBytes = 64 * 1024
)

// BrokerConfig holds broker daemon configuration.
type BrokerConfig struct {
	SocketPath   string
	PolicyPath   string
	AuditLogPath string

	SessionTTL  time.Duration
	IdleTimeout time.Duration

	SessionRateLimit int
	RepoRateLimit    int

	CacheTTL time.Duration
}

// Broker is the host broker daemon. It processes one JSON request per Unix
// socket connection, dispatching mint_credential to the appropriate
// CredentialProvider.
type Broker struct {
	store    *MemorySessionStore
	cache    *MemoryTokenCache
	audit    *FileAuditLogger
	limiter  *RateLimiter
	enforcer *PolicyEnforcer
	registry *providerRegistry
	config   BrokerConfig
	myUID    uint32

	mu           sync.RWMutex
	agentConfigs map[string]map[string]any
}

// NewBroker constructs a broker and validates that every agent in the policy
// has a registered provider and a parseable per-provider config for each
// resource it declares. Fails fast on misconfiguration.
func NewBroker(
	cfg BrokerConfig,
	enforcer *PolicyEnforcer,
	audit *FileAuditLogger,
	providers []CredentialProvider,
) (*Broker, error) {
	store := NewMemorySessionStore()
	if cfg.SessionTTL > 0 {
		store.SessionTTL = cfg.SessionTTL
	}
	if cfg.IdleTimeout > 0 {
		store.IdleTimeout = cfg.IdleTimeout
	}

	registry, err := newProviderRegistry(providers)
	if err != nil {
		return nil, err
	}
	configs, err := registry.validateAndParseConfigs(enforcer.Policy())
	if err != nil {
		return nil, err
	}
	return &Broker{
		store:        store,
		cache:        NewMemoryTokenCache(cfg.CacheTTL),
		audit:        audit,
		limiter:      NewRateLimiter(cfg.SessionRateLimit, cfg.RepoRateLimit),
		enforcer:     enforcer,
		registry:     registry,
		config:       cfg,
		myUID:        uint32(os.Getuid()),
		agentConfigs: configs,
	}, nil
}

// Serve accepts connections and processes one request per connection until
// the context is cancelled.
func (b *Broker) Serve(ctx context.Context, ln net.Listener) error {
	go b.cleanupLoop(ctx)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			continue
		}
		go b.handleConn(conn)
	}
}

func (b *Broker) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.store.Cleanup()
			b.limiter.Cleanup()
		}
	}
}

func (b *Broker) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		b.writeError(conn, ErrCodeBrokerUnavailable, "not a Unix connection")
		return
	}

	peerUID, _, _, err := PeerCred(unixConn)
	if err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "failed to get peer credentials")
		return
	}
	if peerUID != b.myUID {
		b.writeError(conn, ErrCodeUIDMismatch, fmt.Sprintf("peer UID %d does not match broker UID %d", peerUID, b.myUID))
		return
	}

	if err := conn.SetReadDeadline(time.Now().Add(connReadTimeout)); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "set read deadline: "+err.Error())
		return
	}

	var req Request
	if err := json.NewDecoder(io.LimitReader(conn, MaxRequestBytes+1)).Decode(&req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid request: "+err.Error())
		return
	}

	start := time.Now()
	switch req.Method {
	case MethodMintCredential:
		b.handleMintCredential(conn, req.Body, peerUID, start)
	case MethodCreateSession:
		b.handleCreateSession(conn, req.Body, peerUID, start)
	case MethodRevokeSession:
		b.handleRevokeSession(conn, req.Body, peerUID, start)
	case MethodSessionStatus:
		b.handleSessionStatus(conn, req.Body, peerUID, start)
	case MethodHealthCheck:
		b.handleHealthCheck(conn, req.Body)
	default:
		b.writeError(conn, ErrCodeBrokerUnavailable, "unknown method: "+req.Method)
	}
}

func (b *Broker) handleMintCredential(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req CredentialRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid mint_credential body: "+err.Error())
		return
	}

	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, "", "", peerUID, ErrCodeSessionNotFound, err.Error(), "", start)
		b.writeError(conn, ErrCodeSessionNotFound, err.Error())
		return
	}

	if err := b.store.ValidateBinding(req.SessionID, req.BindSecret); err != nil {
		b.auditDenial(EventBindingFailed, req.SessionID, session.AgentName, "", peerUID, ErrCodeBindingMismatch, err.Error(), session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeBindingMismatch, "binding validation failed")
		return
	}

	if !session.IsActive() {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeSessionExpired, "session inactive", session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeSessionExpired, "session is no longer active")
		return
	}

	provider, ok := b.registry.provider(req.CredentialType)
	if !ok {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeUnknownCredType, "credential_type="+req.CredentialType, session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeUnknownCredType, "unknown credential_type: "+req.CredentialType)
		return
	}

	resource, err := ParseResourceURI(req.Resource)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeInvalidResourceURI, err.Error(), session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeInvalidResourceURI, err.Error())
		return
	}

	if expected, ok := b.registry.credentialTypeFor(resource.Provider); !ok || expected != req.CredentialType {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeUnknownCredType,
			fmt.Sprintf("credential_type=%s does not serve resource provider %q", req.CredentialType, resource.Provider), session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeUnknownCredType,
			fmt.Sprintf("credential_type %q does not serve resource provider %q", req.CredentialType, resource.Provider))
		return
	}

	if !resourceInSession(resource, session.Resources) {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeResourceNotAllowed, "resource not in session", session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeResourceNotAllowed, fmt.Sprintf("resource %q is not bound to this session", resource.String()))
		return
	}

	if !b.limiter.Allow(req.SessionID, resource.String()) {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeRateLimited, "rate limit exceeded", session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeRateLimited, "rate limit exceeded")
		return
	}

	cfg, err := b.authorizeAndLoadConfig(session.AgentName, req.CredentialType, resource)
	if err != nil {
		code := codeFor(err)
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, code, err.Error(), session.RunID, start, session.TaskRef)
		b.writeError(conn, code, err.Error())
		return
	}

	cacheKeyPart, err := provider.PrepareMint(req.Params, cfg)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodePermissionDenied, err.Error(), session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodePermissionDenied, err.Error())
		return
	}

	cacheKey := CacheKey{
		Agent:          session.AgentName,
		CredentialType: req.CredentialType,
		Resource:       resource.String(),
		ParamsHash:     cacheKeyPart,
	}

	cached, cacheHit, err := b.cache.GetOrFetch(cacheKey, func() (*CachedCredential, error) {
		result, err := provider.Mint(context.Background(), ProviderMintRequest{
			Resource: resource,
			Params:   req.Params,
			Agent:    session.AgentName,
			Config:   cfg,
		})
		if err != nil {
			return nil, err
		}
		return &CachedCredential{
			Payload:   result.Credential,
			ExpiresAt: result.ExpiresAt,
			CachedAt:  time.Now(),
		}, nil
	})
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeUpstreamError, err.Error(), session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeUpstreamError, err.Error())
		return
	}

	_ = b.store.RecordActivity(req.SessionID)

	eventType := EventTokenMinted
	if cacheHit {
		eventType = EventTokenCacheHit
	}
	b.audit.Log(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  eventType,
		SessionID:  req.SessionID,
		AgentName:  session.AgentName,
		Repo:       resource.Identifier,
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
		Metadata:   correlationMetadata(session.RunID, session.TaskRef),
	})

	b.writeSuccess(conn, &CredentialResponse{
		CredentialType: req.CredentialType,
		Resource:       resource.String(),
		Credential:     cached.Payload,
		ExpiresAt:      cached.ExpiresAt,
	})
}

// authorizeAndLoadConfig holds broker.mu.RLock across the AuthorizeResource
// call and the agent-config lookup so they observe the same snapshot of
// policy and configs even when ReloadPolicy is racing.
func (b *Broker) authorizeAndLoadConfig(agent, credType string, resource ResourceURI) (any, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if err := b.enforcer.AuthorizeResource(agent, resource); err != nil {
		return nil, err
	}
	byAgent, ok := b.agentConfigs[agent]
	if !ok {
		return nil, fmt.Errorf("no provider configuration loaded for agent %q", agent)
	}
	cfg, ok := byAgent[credType]
	if !ok {
		return nil, fmt.Errorf("no %s configuration loaded for agent %q", credType, agent)
	}
	return cfg, nil
}

func codeFor(err error) string {
	switch {
	case errors.Is(err, ErrUnknownCredentialType):
		return ErrCodeUnknownCredType
	case errors.Is(err, ErrResourceNotAllowed):
		return ErrCodeResourceNotAllowed
	default:
		return ErrCodeBrokerUnavailable
	}
}

func resourceInSession(r ResourceURI, set []ResourceURI) bool {
	for _, s := range set {
		if s == r {
			return true
		}
	}
	return false
}

func (b *Broker) handleCreateSession(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req CreateSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid create_session body: "+err.Error())
		return
	}
	if err := correlation.ValidateRunID(req.RunID); err != nil {
		b.auditDenial(EventTokenDenied, "", req.AgentName, "", peerUID, ErrCodeInvalidCorrelation, err.Error(), "", start)
		b.writeError(conn, ErrCodeInvalidCorrelation, err.Error())
		return
	}
	if err := correlation.ValidateTaskRef(req.TaskRef); err != nil {
		b.auditDenial(EventTokenDenied, "", req.AgentName, "", peerUID, ErrCodeInvalidCorrelation, err.Error(), "", start)
		b.writeError(conn, ErrCodeInvalidCorrelation, err.Error())
		return
	}

	if len(req.Resources) == 0 {
		b.auditDenial(EventTokenDenied, "", req.AgentName, "", peerUID, ErrCodeResourceNotAllowed, "no resources requested", req.RunID, start, req.TaskRef)
		b.writeError(conn, ErrCodeResourceNotAllowed, "resources must not be empty")
		return
	}

	for _, raw := range req.Resources {
		parsed, err := ParseResourceURI(raw)
		if err != nil {
			b.auditDenial(EventTokenDenied, "", req.AgentName, raw, peerUID, ErrCodeInvalidResourceURI, err.Error(), req.RunID, start, req.TaskRef)
			b.writeError(conn, ErrCodeInvalidResourceURI, err.Error())
			return
		}
		if err := b.enforcer.AuthorizeResource(req.AgentName, parsed); err != nil {
			code := codeFor(err)
			b.auditDenial(EventTokenDenied, "", req.AgentName, parsed.Identifier, peerUID, code, err.Error(), req.RunID, start, req.TaskRef)
			b.writeError(conn, code, err.Error())
			return
		}
	}

	session, secret, err := b.store.Create(req, peerUID)
	if err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "create session: "+err.Error())
		return
	}

	b.audit.Log(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  EventSessionCreated,
		SessionID:  session.ID,
		AgentName:  req.AgentName,
		Repo:       firstResourceIdentifier(session.Resources),
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
		Metadata:   correlationMetadata(session.RunID, session.TaskRef),
	})

	b.writeSuccess(conn, &CreateSessionResponse{
		SessionID:   session.ID,
		BindSecret:  secret,
		ExpiresAt:   session.ExpiresAt,
		IdleTimeout: DurationString(session.IdleTimeout),
	})
}

func firstResourceIdentifier(rs []ResourceURI) string {
	if len(rs) == 0 {
		return ""
	}
	return rs[0].Identifier
}

func (b *Broker) handleRevokeSession(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req RevokeSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid revoke_session body: "+err.Error())
		return
	}

	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.writeError(conn, ErrCodeSessionNotFound, err.Error())
		return
	}
	if session.PeerUID != peerUID {
		b.auditDenial(EventSessionRevoked, req.SessionID, session.AgentName, "", peerUID, ErrCodeUIDMismatch,
			fmt.Sprintf("revoke denied: session owned by uid %d", session.PeerUID), session.RunID, start, session.TaskRef)
		b.writeError(conn, ErrCodeUIDMismatch, "session is owned by a different user")
		return
	}
	if err := b.store.Revoke(req.SessionID); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, err.Error())
		return
	}

	b.audit.Log(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  EventSessionRevoked,
		SessionID:  req.SessionID,
		AgentName:  session.AgentName,
		Repo:       firstResourceIdentifier(session.Resources),
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
		Metadata:   correlationMetadata(session.RunID, session.TaskRef),
	})

	b.writeSuccess(conn, &RevokeSessionResponse{Revoked: true})
}

func (b *Broker) handleSessionStatus(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req SessionStatusRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid session_status body: "+err.Error())
		return
	}
	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.writeError(conn, ErrCodeSessionNotFound, err.Error())
		return
	}
	if session.PeerUID != peerUID {
		b.writeError(conn, ErrCodeUIDMismatch, "session is owned by a different user")
		return
	}

	resources := make([]string, 0, len(session.Resources))
	for _, r := range session.Resources {
		resources = append(resources, r.String())
	}
	b.writeSuccess(conn, &SessionStatusResponse{
		Active:       session.IsActive(),
		AgentName:    session.AgentName,
		Resources:    resources,
		CreatedAt:    session.CreatedAt,
		ExpiresAt:    session.ExpiresAt,
		LastActivity: session.LastActivity,
		MintCount:    session.MintCount,
	})
}

func (b *Broker) handleHealthCheck(conn net.Conn, body json.RawMessage) {
	var req HealthCheckRequest
	if len(body) != 0 && string(body) != "null" {
		if err := json.Unmarshal(body, &req); err != nil {
			b.writeError(conn, ErrCodeBrokerUnavailable, "invalid health_check body: "+err.Error())
			return
		}
	}
	b.writeSuccess(conn, &HealthCheckResponse{Healthy: true})
}

func (b *Broker) auditDenial(eventType, sessionID, agentName, repo string, peerUID uint32, code, detail, runID string, start time.Time, taskRefs ...string) {
	taskRef := ""
	if len(taskRefs) > 0 {
		taskRef = taskRefs[0]
	}
	b.audit.Log(AuditEvent{
		Timestamp:   time.Now(),
		EventType:   eventType,
		SessionID:   sessionID,
		AgentName:   agentName,
		Repo:        repo,
		PeerUID:     peerUID,
		Success:     false,
		ErrorCode:   code,
		ErrorDetail: detail,
		DurationMS:  time.Since(start).Milliseconds(),
		Metadata:    correlationMetadata(runID, taskRef),
	})
}

func correlationMetadata(runID, taskRef string) map[string]string {
	if runID == "" && taskRef == "" {
		return nil
	}
	metadata := make(map[string]string, 2)
	if runID != "" {
		metadata["run_id"] = runID
	}
	if taskRef != "" {
		metadata["task_ref"] = taskRef
	}
	return metadata
}

func (b *Broker) writeSuccess(conn net.Conn, body interface{}) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "marshal response: "+err.Error())
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(connWriteTimeout))
	_ = json.NewEncoder(conn).Encode(Response{OK: true, Body: bodyJSON})
}

func (b *Broker) writeError(conn net.Conn, code, message string) {
	_ = conn.SetWriteDeadline(time.Now().Add(connWriteTimeout))
	_ = json.NewEncoder(conn).Encode(Response{
		OK:    false,
		Error: &ErrorResponse{Code: code, Message: message},
	})
}

// ReloadPolicy parses and validates the policy file, computes new per-agent
// provider configs, then atomically swaps both into place. If any step fails
// the broker continues with the previous policy and configs unchanged. On
// success the credential cache is cleared because changed provider configs
// may have invalidated cached upstream identities.
func (b *Broker) ReloadPolicy() error {
	data, err := os.ReadFile(b.config.PolicyPath)
	if err != nil {
		return fmt.Errorf("policy reload: read %s: %w", b.config.PolicyPath, err)
	}
	p, err := policy.ParsePolicy(data)
	if err != nil {
		return fmt.Errorf("policy reload: %w", err)
	}
	if result := policy.Validate(p); result.Errors.HasErrors() {
		return fmt.Errorf("policy reload: validation failed: %s", result.Errors.Error())
	}
	newConfigs, err := b.registry.validateAndParseConfigs(p)
	if err != nil {
		return fmt.Errorf("policy reload: %w", err)
	}

	b.mu.Lock()
	b.enforcer.SetPolicy(p)
	b.agentConfigs = newConfigs
	b.mu.Unlock()
	b.cache.Clear()
	return nil
}
