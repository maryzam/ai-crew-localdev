package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
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
	store        *MemorySessionStore
	cache        *MemoryTokenCache
	audit        *FileAuditLogger
	limiter      *RateLimiter
	enforcer     *PolicyEnforcer
	providers    map[string]CredentialProvider
	uriToType    map[string]string
	agentConfigs map[string]map[string]any
	config       BrokerConfig
	myUID        uint32
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

	b := &Broker{
		store:     store,
		cache:     NewMemoryTokenCache(cfg.CacheTTL),
		audit:     audit,
		limiter:   NewRateLimiter(cfg.SessionRateLimit, cfg.RepoRateLimit),
		enforcer:  enforcer,
		providers: map[string]CredentialProvider{},
		uriToType: map[string]string{},
		config:    cfg,
		myUID:     uint32(os.Getuid()),
	}
	for _, p := range providers {
		b.providers[p.Type()] = p
		b.uriToType[p.URIProvider()] = p.Type()
	}
	if err := b.loadAgentConfigs(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Broker) loadAgentConfigs() error {
	policyFile := b.enforcer.Policy()
	configs := make(map[string]map[string]any, len(policyFile.Agents))

	for agent, ap := range policyFile.Agents {
		needed := map[string]struct{}{}
		for _, raw := range ap.Resources {
			uri, err := ParseResourceURI(raw)
			if err != nil {
				return fmt.Errorf("policy: agent %q resource %q: %w", agent, raw, err)
			}
			needed[uri.Provider] = struct{}{}
		}

		for uriProvider := range needed {
			credType, ok := b.uriToType[uriProvider]
			if !ok {
				return fmt.Errorf("policy: agent %q: no provider registered for %s resources", agent, uriProvider)
			}
			section, ok := b.enforcer.ProviderSection(agent, uriProvider)
			if !ok {
				return fmt.Errorf("policy: agent %q declares %s resources but providers.%s is missing", agent, uriProvider, uriProvider)
			}
			cfg, err := b.providers[credType].ParseConfig(agent, section)
			if err != nil {
				return fmt.Errorf("policy: %w", err)
			}
			if configs[agent] == nil {
				configs[agent] = map[string]any{}
			}
			configs[agent][credType] = cfg
		}
	}
	b.agentConfigs = configs
	return nil
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

	if err := conn.SetWriteDeadline(time.Now().Add(connWriteTimeout)); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "set write deadline: "+err.Error())
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
		b.auditDenial(EventTokenDenied, req.SessionID, "", "", peerUID, ErrCodeSessionNotFound, err.Error(), start)
		b.writeError(conn, ErrCodeSessionNotFound, err.Error())
		return
	}

	if err := b.store.ValidateBinding(req.SessionID, req.BindSecret); err != nil {
		b.auditDenial(EventBindingFailed, req.SessionID, session.AgentName, "", peerUID, ErrCodeBindingMismatch, err.Error(), start)
		b.writeError(conn, ErrCodeBindingMismatch, "binding validation failed")
		return
	}

	if !session.IsActive() {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeSessionExpired, "session inactive", start)
		b.writeError(conn, ErrCodeSessionExpired, "session is no longer active")
		return
	}

	provider, ok := b.providers[req.CredentialType]
	if !ok {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeUnknownCredType, "credential_type="+req.CredentialType, start)
		b.writeError(conn, ErrCodeUnknownCredType, "unknown credential_type: "+req.CredentialType)
		return
	}

	resource, err := ParseResourceURI(req.Resource)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeInvalidResourceURI, err.Error(), start)
		b.writeError(conn, ErrCodeInvalidResourceURI, err.Error())
		return
	}

	if !resourceInSession(resource, session.Resources) {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeResourceNotAllowed, "resource not in session", start)
		b.writeError(conn, ErrCodeResourceNotAllowed, fmt.Sprintf("resource %q is not bound to this session", resource.String()))
		return
	}

	if !b.limiter.Allow(req.SessionID, resource.String()) {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeRateLimited, "rate limit exceeded", start)
		b.writeError(conn, ErrCodeRateLimited, "rate limit exceeded")
		return
	}

	if err := b.enforcer.AuthorizeResource(session.AgentName, resource); err != nil {
		code := ErrCodeResourceNotAllowed
		if errors.Is(err, ErrUnknownCredentialType) {
			code = ErrCodeUnknownCredType
		}
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, code, err.Error(), start)
		b.writeError(conn, code, err.Error())
		return
	}

	cfg, err := b.configFor(session.AgentName, req.CredentialType)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeBrokerUnavailable, err.Error(), start)
		b.writeError(conn, ErrCodeBrokerUnavailable, err.Error())
		return
	}

	cacheKeyPart, err := provider.PrepareMint(req.Params, cfg)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodePermissionDenied, err.Error(), start)
		b.writeError(conn, ErrCodePermissionDenied, err.Error())
		return
	}

	cacheKey := CacheKey{
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
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeUpstreamError, err.Error(), start)
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
	})

	b.writeSuccess(conn, &CredentialResponse{
		CredentialType: req.CredentialType,
		Resource:       resource.String(),
		Credential:     cached.Payload,
		ExpiresAt:      cached.ExpiresAt,
	})
}

func (b *Broker) configFor(agent, credType string) (any, error) {
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

	if len(req.Resources) == 0 {
		b.auditDenial(EventTokenDenied, "", req.AgentName, "", peerUID, ErrCodeResourceNotAllowed, "no resources requested", start)
		b.writeError(conn, ErrCodeResourceNotAllowed, "resources must not be empty")
		return
	}

	for _, raw := range req.Resources {
		parsed, err := ParseResourceURI(raw)
		if err != nil {
			b.auditDenial(EventTokenDenied, "", req.AgentName, raw, peerUID, ErrCodeInvalidResourceURI, err.Error(), start)
			b.writeError(conn, ErrCodeInvalidResourceURI, err.Error())
			return
		}
		if err := b.enforcer.AuthorizeResource(req.AgentName, parsed); err != nil {
			code := ErrCodeResourceNotAllowed
			if errors.Is(err, ErrUnknownCredentialType) {
				code = ErrCodeUnknownCredType
			}
			b.auditDenial(EventTokenDenied, "", req.AgentName, parsed.Identifier, peerUID, code, err.Error(), start)
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
		Repo:       firstResourceIdentifier(session.Resources),
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
	_ = json.NewEncoder(conn).Encode(Response{OK: true, Body: bodyJSON})
}

func (b *Broker) writeError(conn net.Conn, code, message string) {
	_ = json.NewEncoder(conn).Encode(Response{
		OK:    false,
		Error: &ErrorResponse{Code: code, Message: message},
	})
}

// ReloadPolicy reloads the policy file and re-validates agent configurations
// against registered providers.
func (b *Broker) ReloadPolicy() error {
	if err := b.enforcer.Reload(b.config.PolicyPath); err != nil {
		return err
	}
	return b.loadAgentConfigs()
}
