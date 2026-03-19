package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
)

const (
	// connReadTimeout is the maximum time to read a request from a connection.
	connReadTimeout = 5 * time.Second

	// connWriteTimeout is the maximum time to write a response.
	connWriteTimeout = 5 * time.Second

	// cleanupInterval is how often expired sessions are purged.
	cleanupInterval = 5 * time.Minute
)

// BrokerConfig holds all configuration for the broker daemon.
type BrokerConfig struct {
	SocketPath   string
	PolicyPath   string
	AuditLogPath string

	SessionTTL  time.Duration
	IdleTimeout time.Duration

	SessionRateLimit int
	RepoRateLimit    int

	CacheTTL time.Duration

	// GitHubBaseURL overrides the GitHub API base URL (for testing).
	GitHubBaseURL string
}

// Broker is the host broker daemon. It processes one JSON request per
// Unix socket connection and enforces policy, rate limits, and audit.
type Broker struct {
	signer   *Signer
	store    *MemorySessionStore
	cache    *MemoryTokenCache
	audit    AuditLogger
	limiter  *RateLimiter
	enforcer *PolicyEnforcer
	github   *GitHubClient
	idents   *identity.IdentitiesFile
	config   BrokerConfig
	myUID    uint32
}

// NewBroker constructs a broker from the given configuration, identity,
// and policy files.
func NewBroker(
	cfg BrokerConfig,
	idents *identity.IdentitiesFile,
	enforcer *PolicyEnforcer,
	signer *Signer,
	audit AuditLogger,
) *Broker {
	store := NewMemorySessionStore()
	if cfg.SessionTTL > 0 {
		store.SessionTTL = cfg.SessionTTL
	}
	if cfg.IdleTimeout > 0 {
		store.IdleTimeout = cfg.IdleTimeout
	}

	return &Broker{
		signer:   signer,
		store:    store,
		cache:    NewMemoryTokenCache(cfg.CacheTTL),
		audit:    audit,
		limiter:  NewRateLimiter(cfg.SessionRateLimit, cfg.RepoRateLimit),
		enforcer: enforcer,
		github:   NewGitHubClient(cfg.GitHubBaseURL),
		idents:   idents,
		config:   cfg,
		myUID:    uint32(os.Getuid()),
	}
}

// Serve accepts connections on the listener and processes one request
// per connection. It blocks until the context is cancelled.
func (b *Broker) Serve(ctx context.Context, ln net.Listener) error {
	// Start background session cleanup.
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

	// Extract peer credentials immediately.
	peerUID, _, _, err := PeerCred(unixConn)
	if err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "failed to get peer credentials")
		return
	}

	// Verify UID.
	if peerUID != b.myUID {
		b.writeError(conn, ErrCodeUIDMismatch,
			fmt.Sprintf("peer UID %d does not match broker UID %d", peerUID, b.myUID))
		return
	}

	if err := conn.SetReadDeadline(time.Now().Add(connReadTimeout)); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "set read deadline: "+err.Error())
		return
	}

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid request: "+err.Error())
		return
	}

	if err := conn.SetWriteDeadline(time.Now().Add(connWriteTimeout)); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "set write deadline: "+err.Error())
		return
	}

	start := time.Now()

	switch req.Method {
	case MethodMintToken:
		b.handleMintToken(conn, req.Body, peerUID, start)
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

// ---- Method handlers -------------------------------------------------------

func (b *Broker) handleMintToken(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req TokenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid mint_token body: "+err.Error())
		return
	}

	// Look up session.
	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, "", "", peerUID, ErrCodeSessionNotFound, err.Error(), start)
		b.writeError(conn, ErrCodeSessionNotFound, err.Error())
		return
	}

	// Validate binding.
	if err := b.store.ValidateBinding(req.SessionID, req.BindSecret); err != nil {
		b.auditDenial(EventBindingFailed, req.SessionID, session.AgentName, session.Repo, peerUID, ErrCodeBindingMismatch, err.Error(), start)
		b.writeError(conn, ErrCodeBindingMismatch, "binding validation failed")
		return
	}

	// Check session is active.
	if !session.IsActive() {
		code := ErrCodeSessionExpired
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, session.Repo, peerUID, code, "session inactive", start)
		b.writeError(conn, code, "session is no longer active")
		return
	}

	// Verify requested repo matches session-bound repo (phase 1: single-repo).
	if req.Repo != session.Repo {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, req.Repo, peerUID, ErrCodeRepoNotAllowed, "repo mismatch", start)
		b.writeError(conn, ErrCodeRepoNotAllowed,
			fmt.Sprintf("requested repo %q does not match session repo %q", req.Repo, session.Repo))
		return
	}

	// Check rate limits.
	if !b.limiter.Allow(req.SessionID, req.Repo) {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, req.Repo, peerUID, ErrCodeRateLimited, "rate limit exceeded", start)
		b.writeError(conn, ErrCodeRateLimited, "rate limit exceeded")
		return
	}

	// Merge permissions.
	perms, err := MergePermissions(session.Permissions, req.Permissions)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, req.Repo, peerUID, ErrCodePermissionDenied, err.Error(), start)
		b.writeError(conn, ErrCodePermissionDenied, err.Error())
		return
	}

	// Re-authorize against the current policy (may have changed via SIGHUP reload).
	if err := b.enforcer.Authorize(session.AgentName, req.Repo, perms); err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, req.Repo, peerUID, ErrCodeRepoNotAllowed, "policy re-check: "+err.Error(), start)
		b.writeError(conn, ErrCodeRepoNotAllowed, "denied by current policy: "+err.Error())
		return
	}

	// Resolve installation ID.
	installID, err := b.enforcer.InstallationID(session.AgentName)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, req.Repo, peerUID, ErrCodeBrokerUnavailable, err.Error(), start)
		b.writeError(conn, ErrCodeBrokerUnavailable, err.Error())
		return
	}

	// Resolve app ID for JWT signing.
	appID := b.appIDForAgent(session.AgentName)
	if appID == "" {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, req.Repo, peerUID, ErrCodeBrokerUnavailable, "no app ID for agent", start)
		b.writeError(conn, ErrCodeBrokerUnavailable, "no app ID configured for agent")
		return
	}

	// Build cache key.
	cacheKey := CacheKey{
		InstallationID: installID,
		Repo:           req.Repo,
		Permissions:    SerializePermissions(perms),
	}

	// Fetch token (with singleflight coalescing and cache).
	token, cacheHit, err := b.cache.GetOrFetch(cacheKey, func() (*CachedToken, error) {
		jwt, err := b.signer.SignJWT(appID)
		if err != nil {
			return nil, fmt.Errorf("sign JWT: %w", err)
		}

		resp, err := b.github.MintInstallationToken(
			context.Background(), jwt, installID, req.Repo, perms,
		)
		if err != nil {
			return nil, fmt.Errorf("mint token: %w", err)
		}

		return &CachedToken{
			Token:     resp.Token,
			ExpiresAt: resp.ExpiresAt,
			CachedAt:  time.Now(),
		}, nil
	})
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, req.Repo, peerUID, ErrCodeUpstreamError, err.Error(), start)
		b.writeError(conn, ErrCodeUpstreamError, err.Error())
		return
	}

	// Record activity.
	_ = b.store.RecordActivity(req.SessionID)

	// Audit.
	eventType := EventTokenMinted
	if cacheHit {
		eventType = EventTokenCacheHit
	}
	b.audit.Log(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  eventType,
		SessionID:  req.SessionID,
		AgentName:  session.AgentName,
		Repo:       req.Repo,
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
	})

	b.writeSuccess(conn, &TokenResponse{
		Token:     token.Token,
		ExpiresAt: token.ExpiresAt,
		Repo:      req.Repo,
	})
}

func (b *Broker) handleCreateSession(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req CreateSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid create_session body: "+err.Error())
		return
	}

	// Policy check: agent must be allowed to access this repo.
	if err := b.enforcer.Authorize(req.AgentName, req.Repo, req.RequestedPermissions); err != nil {
		b.auditDenial(EventTokenDenied, "", req.AgentName, req.Repo, peerUID, ErrCodeRepoNotAllowed, err.Error(), start)
		b.writeError(conn, ErrCodeRepoNotAllowed, err.Error())
		return
	}

	// If no permissions requested, use defaults from policy.
	if len(req.RequestedPermissions) == 0 {
		defaults, err := b.enforcer.DefaultPermissions(req.AgentName)
		if err != nil {
			b.writeError(conn, ErrCodeBrokerUnavailable, err.Error())
			return
		}
		req.RequestedPermissions = defaults
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
		Repo:       req.Repo,
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
	})

	b.writeSuccess(conn, &CreateSessionResponse{
		SessionID:   session.ID,
		BindSecret:  secret,
		ExpiresAt:   session.ExpiresAt,
		IdleTimeout: DurationString(session.IdleTimeout),
	})
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

	if err := b.store.ValidateBinding(req.SessionID, req.BindSecret); err != nil {
		b.writeError(conn, ErrCodeBindingMismatch, "binding validation failed")
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
		Repo:       session.Repo,
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
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

	if err := b.store.ValidateBinding(req.SessionID, req.BindSecret); err != nil {
		b.writeError(conn, ErrCodeBindingMismatch, "binding validation failed")
		return
	}

	// session_status is read-only; it must NOT advance LastActivity.
	b.writeSuccess(conn, &SessionStatusResponse{
		Active:          session.IsActive(),
		AgentName:       session.AgentName,
		Repo:            session.Repo,
		CreatedAt:       session.CreatedAt,
		ExpiresAt:       session.ExpiresAt,
		LastActivity:    session.LastActivity,
		TokenMintsCount: session.TokenMintCount,
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

// ---- Helpers ---------------------------------------------------------------

func (b *Broker) appIDForAgent(agentName string) string {
	agent, ok := b.idents.Agents[agentName]
	if !ok {
		return ""
	}
	return agent.AppID
}

func (b *Broker) auditDenial(eventType, sessionID, agentName, repo string, peerUID uint32, code, detail string, start time.Time) {
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
	})
}

func (b *Broker) writeSuccess(conn net.Conn, body interface{}) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "marshal response: "+err.Error())
		return
	}
	resp := Response{OK: true, Body: bodyJSON}
	_ = json.NewEncoder(conn).Encode(resp)
}

func (b *Broker) writeError(conn net.Conn, code, message string) {
	resp := Response{
		OK:    false,
		Error: &ErrorResponse{Code: code, Message: message},
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

// ReloadPolicy triggers a policy reload from the configured path.
func (b *Broker) ReloadPolicy() error {
	return b.enforcer.Reload(b.config.PolicyPath)
}
