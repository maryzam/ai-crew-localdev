package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/store"
	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
)

const (
	connReadTimeout  = 5 * time.Second
	connWriteTimeout = 5 * time.Second
	cleanupInterval  = 5 * time.Minute

	MaxControlRequestBytes  = 64 * 1024
	MaxRequestBytes         = api.MaxTelemetryPayloadBytes + MaxControlRequestBytes
	telemetryPublishTimeout = 3 * time.Second
)

type BrokerConfig struct {
	SocketPath     string
	IdentitiesPath string
	PolicyPath     string
	AuditLogPath   string

	SessionTTL  time.Duration
	IdleTimeout time.Duration

	SessionRateLimit           int
	RepoRateLimit              int
	TelemetrySessionRateLimit  int
	TelemetryResourceRateLimit int

	CacheTTL time.Duration
}

type Broker struct {
	store            *MemorySessionStore
	cache            *MemoryTokenCache
	audit            AuditSink
	limiter          *RateLimiter
	telemetryLimiter *RateLimiter
	enforcer         *PolicyEnforcer
	registry         *providerRegistry
	config           BrokerConfig
	myUID            uint32

	mu           sync.RWMutex
	agentConfigs map[string]map[string]any
}

func NewBroker(
	cfg BrokerConfig,
	enforcer *PolicyEnforcer,
	audit AuditSink,
	providers []port.Provider,
) (*Broker, error) {
	if audit == nil {
		return nil, fmt.Errorf("audit sink is required")
	}
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
	enforcer.AddKnownProviders(registry.uriProviders()...)
	configs, err := registry.validateAndParseConfigs(enforcer.Policy())
	if err != nil {
		return nil, err
	}
	return &Broker{
		store:            store,
		cache:            NewMemoryTokenCache(cfg.CacheTTL),
		audit:            audit,
		limiter:          NewRateLimiter(cfg.SessionRateLimit, cfg.RepoRateLimit),
		telemetryLimiter: NewTelemetryRateLimiter(cfg.TelemetrySessionRateLimit, cfg.TelemetryResourceRateLimit),
		enforcer:         enforcer,
		registry:         registry,
		config:           cfg,
		myUID:            uint32(os.Getuid()),
		agentConfigs:     configs,
	}, nil
}

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
			b.cleanupSessions()
			b.limiter.Cleanup()
			b.telemetryLimiter.Cleanup()
		}
	}
}

func (b *Broker) cleanupSessions() {
	expired := b.store.Cleanup(func(session *Session) bool {
		if session.Revoked {
			return true
		}
		err := b.audit.Record(AuditEvent{Timestamp: time.Now(), EventType: EventSessionExpireRequested, SessionID: session.ID, AgentName: session.AgentName, Repo: firstResourceIdentifier(session.Resources), PeerUID: session.PeerUID, Success: true, Metadata: correlationMetadata(session.RunID, session.TaskRef)})
		return err == nil
	})
	for _, session := range expired {
		_ = b.audit.Record(AuditEvent{Timestamp: time.Now(), EventType: EventSessionExpired, SessionID: session.ID, AgentName: session.AgentName, Repo: firstResourceIdentifier(session.Resources), PeerUID: session.PeerUID, Success: true, Metadata: correlationMetadata(session.RunID, session.TaskRef)})
	}
}

func (b *Broker) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "not a Unix connection")
		return
	}

	peerUID, _, _, err := PeerCred(unixConn)
	if err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "failed to get peer credentials")
		return
	}
	if peerUID != b.myUID {
		b.writeError(conn, api.ErrCodeUIDMismatch, fmt.Sprintf("peer UID %d does not match broker UID %d", peerUID, b.myUID))
		return
	}

	if err := conn.SetReadDeadline(time.Now().Add(connReadTimeout)); err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "set read deadline: "+err.Error())
		return
	}

	var req api.Request
	if err := json.NewDecoder(io.LimitReader(conn, MaxRequestBytes+1)).Decode(&req); err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "invalid request: "+err.Error())
		return
	}
	if req.Method != api.MethodPublishTelemetry && len(req.Body) > MaxControlRequestBytes {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, fmt.Sprintf("control request exceeds %d bytes", MaxControlRequestBytes))
		return
	}

	start := time.Now()
	switch req.Method {
	case api.MethodMintCredential:
		b.handleMintCredential(conn, req.Body, peerUID, start)
	case api.MethodPublishTelemetry:
		b.handlePublishTelemetry(conn, req.Body, peerUID, start)
	case api.MethodAuthorizeResources:
		b.handleAuthorizeResources(conn, req.Body, peerUID, start)
	case api.MethodCreateSession:
		b.handleCreateSession(conn, req.Body, peerUID, start)
	case api.MethodRevokeSession:
		b.handleRevokeSession(conn, req.Body, peerUID, start)
	case api.MethodSessionStatus:
		b.handleSessionStatus(conn, req.Body, peerUID, start)
	case api.MethodHealthCheck:
		b.handleHealthCheck(conn, req.Body)
	default:
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "unknown method: "+req.Method)
	}
}

func (b *Broker) handlePublishTelemetry(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req api.PublishTelemetryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, api.ErrCodeInvalidPayload, "invalid publish_telemetry body: "+err.Error())
		return
	}
	if len(req.Payload) == 0 || len(req.Payload) > api.MaxTelemetryPayloadBytes || !json.Valid(req.Payload) {
		b.writeError(conn, api.ErrCodeInvalidPayload, fmt.Sprintf("telemetry payload must be valid JSON within %d bytes", api.MaxTelemetryPayloadBytes))
		return
	}

	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.deny(conn, EventTelemetryDenied, req.SessionID, "", "", peerUID, api.ErrCodeSessionNotFound, err.Error(), err.Error(), "", "", start)
		return
	}
	if err := b.store.ValidateBinding(req.SessionID, req.BindSecret); err != nil {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeBindingMismatch, err.Error(), "binding validation failed", session.RunID, session.TaskRef, start)
		return
	}
	if !session.IsActive() {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeSessionExpired, "session inactive", "session is no longer active", session.RunID, session.TaskRef, start)
		return
	}
	resource, err := api.ParseResourceURI(req.Resource)
	if err != nil {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeInvalidResourceURI, err.Error(), err.Error(), session.RunID, session.TaskRef, start)
		return
	}
	if !resourceInSession(resource, session.Resources) {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeResourceNotAllowed, "resource not in session", fmt.Sprintf("resource %q is not bound to this session", resource.String()), session.RunID, session.TaskRef, start)
		return
	}
	provider, ok := b.registry.telemetryProvider(resource.Provider)
	if !ok {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeUnsupportedCapability, "provider has no telemetry capability", fmt.Sprintf("provider %q does not support telemetry egress", resource.Provider), session.RunID, session.TaskRef, start)
		return
	}
	if err := provider.ValidateResource(resource); err != nil {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeInvalidResourceURI, err.Error(), err.Error(), session.RunID, session.TaskRef, start)
		return
	}
	if !b.telemetryLimiter.Allow(req.SessionID, resource.String()) {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeRateLimited, "rate limit exceeded", "rate limit exceeded", session.RunID, session.TaskRef, start)
		return
	}
	cfg, err := b.authorizeAndLoadConfig(session.AgentName, resource.Provider, resource)
	if err != nil {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, codeFor(err), err.Error(), err.Error(), session.RunID, session.TaskRef, start)
		return
	}
	metadata := telemetryAuditMetadata(session.RunID, session.TaskRef, req.Payload)
	if err := b.audit.Record(AuditEvent{Timestamp: time.Now(), EventType: EventTelemetryPublishRequested, SessionID: req.SessionID, AgentName: session.AgentName, Repo: resource.Identifier, PeerUID: peerUID, Success: true, DurationMS: time.Since(start).Milliseconds(), Metadata: metadata}); err != nil {
		b.writeAuditError(conn, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), telemetryPublishTimeout)
	err = provider.PublishTelemetry(ctx, port.ProviderTelemetryRequest{Resource: resource, Agent: session.AgentName, Config: cfg, Payload: req.Payload})
	cancel()
	if err != nil {
		b.deny(conn, EventTelemetryDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeUpstreamError, err.Error(), "telemetry egress failed", session.RunID, session.TaskRef, start)
		return
	}
	_ = b.store.RecordActivity(req.SessionID)
	if err := b.audit.Record(AuditEvent{Timestamp: time.Now(), EventType: EventTelemetryPublished, SessionID: req.SessionID, AgentName: session.AgentName, Repo: resource.Identifier, PeerUID: peerUID, Success: true, DurationMS: time.Since(start).Milliseconds(), Metadata: metadata}); err != nil {
		b.writeAuditError(conn, err)
		return
	}
	b.writeSuccess(conn, &api.PublishTelemetryResponse{AcceptedBytes: len(req.Payload)})
}

func telemetryAuditMetadata(runID, taskRef string, payload []byte) map[string]string {
	metadata := correlationMetadata(runID, taskRef)
	if metadata == nil {
		metadata = make(map[string]string, 2)
	}
	sum := sha256.Sum256(payload)
	metadata["payload_bytes"] = fmt.Sprintf("%d", len(payload))
	metadata["payload_sha256"] = fmt.Sprintf("%x", sum)
	return metadata
}

func (b *Broker) handleMintCredential(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req api.CredentialRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "invalid mint_credential body: "+err.Error())
		return
	}

	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.deny(conn, EventTokenDenied, req.SessionID, "", "", peerUID, api.ErrCodeSessionNotFound, err.Error(), err.Error(), "", "", start)
		return
	}

	if err := b.store.ValidateBinding(req.SessionID, req.BindSecret); err != nil {
		b.deny(conn, EventBindingFailed, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeBindingMismatch, err.Error(), "binding validation failed", session.RunID, session.TaskRef, start)
		return
	}

	if !session.IsActive() {
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeSessionExpired, "session inactive", "session is no longer active", session.RunID, session.TaskRef, start)
		return
	}

	provider, ok := b.registry.provider(req.CredentialType)
	if !ok {
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeUnknownCredType, "credential_type="+req.CredentialType, "unknown credential_type: "+req.CredentialType, session.RunID, session.TaskRef, start)
		return
	}

	resource, err := api.ParseResourceURI(req.Resource)
	if err != nil {
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeInvalidResourceURI, err.Error(), err.Error(), session.RunID, session.TaskRef, start)
		return
	}

	if expected, ok := b.registry.credentialTypeFor(resource.Provider); !ok || expected != req.CredentialType {
		detail := fmt.Sprintf("credential_type=%s does not serve resource provider %q", req.CredentialType, resource.Provider)
		message := fmt.Sprintf("credential_type %q does not serve resource provider %q", req.CredentialType, resource.Provider)
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeUnknownCredType, detail, message, session.RunID, session.TaskRef, start)
		return
	}

	if !resourceInSession(resource, session.Resources) {
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeResourceNotAllowed, "resource not in session", fmt.Sprintf("resource %q is not bound to this session", resource.String()), session.RunID, session.TaskRef, start)
		return
	}

	if !b.limiter.Allow(req.SessionID, resource.String()) {
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeRateLimited, "rate limit exceeded", "rate limit exceeded", session.RunID, session.TaskRef, start)
		return
	}

	cfg, err := b.authorizeAndLoadConfig(session.AgentName, provider.URIProvider(), resource)
	if err != nil {
		code := codeFor(err)
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, code, err.Error(), err.Error(), session.RunID, session.TaskRef, start)
		return
	}

	cacheKeyPart, err := provider.PrepareMint(req.Params, cfg)
	if err != nil {
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodePermissionDenied, err.Error(), err.Error(), session.RunID, session.TaskRef, start)
		return
	}

	cacheKey := CacheKey{
		Agent:          session.AgentName,
		CredentialType: req.CredentialType,
		Resource:       resource.String(),
		ParamsHash:     cacheKeyPart,
	}
	if err := b.audit.Record(AuditEvent{Timestamp: time.Now(), EventType: EventTokenMintRequested, SessionID: req.SessionID, AgentName: session.AgentName, Repo: resource.Identifier, PeerUID: peerUID, Success: true, DurationMS: time.Since(start).Milliseconds(), Metadata: correlationMetadata(session.RunID, session.TaskRef)}); err != nil {
		b.writeAuditError(conn, err)
		return
	}

	cached, cacheHit, err := b.cache.GetOrFetch(cacheKey, func() (*CachedCredential, error) {
		result, err := provider.Mint(context.Background(), port.ProviderMintRequest{
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
		b.deny(conn, EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, api.ErrCodeUpstreamError, err.Error(), err.Error(), session.RunID, session.TaskRef, start)
		return
	}

	_ = b.store.RecordActivity(req.SessionID)

	eventType := EventTokenMinted
	if cacheHit {
		eventType = EventTokenCacheHit
	}
	if err := b.audit.Record(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  eventType,
		SessionID:  req.SessionID,
		AgentName:  session.AgentName,
		Repo:       resource.Identifier,
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
		Metadata:   correlationMetadata(session.RunID, session.TaskRef),
	}); err != nil {
		b.writeAuditError(conn, err)
		return
	}

	b.writeSuccess(conn, &api.CredentialResponse{
		CredentialType: req.CredentialType,
		Resource:       resource.String(),
		Credential:     cached.Payload,
		ExpiresAt:      cached.ExpiresAt,
	})
}

func (b *Broker) authorizeAndLoadConfig(agent, providerName string, resource api.ResourceURI) (any, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if err := b.enforcer.AuthorizeResource(agent, resource); err != nil {
		return nil, err
	}
	byAgent, ok := b.agentConfigs[agent]
	if !ok {
		return nil, fmt.Errorf("no provider configuration loaded for agent %q", agent)
	}
	cfg, ok := byAgent[providerName]
	if !ok {
		return nil, fmt.Errorf("no %s configuration loaded for agent %q", providerName, agent)
	}
	return cfg, nil
}

func codeFor(err error) string {
	switch {
	case errors.Is(err, ErrUnknownCredentialType):
		return api.ErrCodeUnknownCredType
	case errors.Is(err, ErrResourceNotAllowed):
		return api.ErrCodeResourceNotAllowed
	default:
		return api.ErrCodeBrokerUnavailable
	}
}

func resourceInSession(r api.ResourceURI, set []api.ResourceURI) bool {
	for _, s := range set {
		if s == r {
			return true
		}
	}
	return false
}

func (b *Broker) handleCreateSession(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req api.CreateSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "invalid create_session body: "+err.Error())
		return
	}
	if err := correlation.ValidateRunID(req.RunID); err != nil {
		b.deny(conn, EventTokenDenied, "", req.AgentName, "", peerUID, api.ErrCodeInvalidCorrelation, err.Error(), err.Error(), "", "", start)
		return
	}
	if err := correlation.ValidateTaskRef(req.TaskRef); err != nil {
		b.deny(conn, EventTokenDenied, "", req.AgentName, "", peerUID, api.ErrCodeInvalidCorrelation, err.Error(), err.Error(), "", "", start)
		return
	}

	if failure := b.authorizeResources(req.AgentName, req.Resources); failure.err != nil {
		b.deny(conn, EventTokenDenied, "", req.AgentName, failure.resource, peerUID, failure.code, failure.err.Error(), failure.err.Error(), req.RunID, req.TaskRef, start)
		return
	}

	session, secret, err := b.store.Create(req, peerUID)
	if err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "create session: "+err.Error())
		return
	}

	if err := b.audit.Record(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  EventSessionCreated,
		SessionID:  session.ID,
		AgentName:  req.AgentName,
		Repo:       firstResourceIdentifier(session.Resources),
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
		Metadata:   correlationMetadata(session.RunID, session.TaskRef),
	}); err != nil {
		_ = b.store.Revoke(session.ID)
		b.writeAuditError(conn, err)
		return
	}

	b.writeSuccess(conn, &api.CreateSessionResponse{
		SessionID:   session.ID,
		BindSecret:  secret,
		ExpiresAt:   session.ExpiresAt,
		IdleTimeout: api.DurationString(session.IdleTimeout),
	})
}

func (b *Broker) handleAuthorizeResources(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req api.AuthorizeResourcesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, api.ErrCodeInvalidPayload, "invalid authorize_resources body: "+err.Error())
		return
	}
	if err := correlation.ValidateRunID(req.RunID); err != nil {
		b.deny(conn, EventResourcesDenied, "", req.AgentName, "", peerUID, api.ErrCodeInvalidCorrelation, err.Error(), err.Error(), "", "", start)
		return
	}
	if err := correlation.ValidateTaskRef(req.TaskRef); err != nil {
		b.deny(conn, EventResourcesDenied, "", req.AgentName, "", peerUID, api.ErrCodeInvalidCorrelation, err.Error(), err.Error(), "", "", start)
		return
	}
	if failure := b.authorizeResources(req.AgentName, req.Resources); failure.err != nil {
		b.deny(conn, EventResourcesDenied, "", req.AgentName, failure.resource, peerUID, failure.code, failure.err.Error(), failure.err.Error(), req.RunID, req.TaskRef, start)
		return
	}
	if err := b.audit.Record(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  EventResourcesAuthorized,
		AgentName:  req.AgentName,
		Repo:       firstRawResourceIdentifier(req.Resources),
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
		Metadata:   correlationMetadata(req.RunID, req.TaskRef),
	}); err != nil {
		b.writeAuditError(conn, err)
		return
	}
	b.writeSuccess(conn, api.AuthorizeResourcesResponse{})
}

type resourceAuthorizationFailure struct {
	resource string
	code     string
	err      error
}

func (b *Broker) authorizeResources(agentName string, resources []string) resourceAuthorizationFailure {
	if len(resources) == 0 {
		return resourceAuthorizationFailure{code: api.ErrCodeResourceNotAllowed, err: fmt.Errorf("resources must not be empty")}
	}
	for _, raw := range resources {
		parsed, err := api.ParseResourceURI(raw)
		if err != nil {
			return resourceAuthorizationFailure{resource: raw, code: api.ErrCodeInvalidResourceURI, err: err}
		}
		if err := b.enforcer.AuthorizeResource(agentName, parsed); err != nil {
			return resourceAuthorizationFailure{resource: parsed.Identifier, code: codeFor(err), err: err}
		}
	}
	return resourceAuthorizationFailure{}
}

func firstResourceIdentifier(rs []api.ResourceURI) string {
	if len(rs) == 0 {
		return ""
	}
	return rs[0].Identifier
}

func firstRawResourceIdentifier(rs []string) string {
	if len(rs) == 0 {
		return ""
	}
	parsed, err := api.ParseResourceURI(rs[0])
	if err != nil {
		return rs[0]
	}
	return parsed.Identifier
}

func (b *Broker) handleRevokeSession(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req api.RevokeSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "invalid revoke_session body: "+err.Error())
		return
	}

	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.writeError(conn, api.ErrCodeSessionNotFound, err.Error())
		return
	}
	if session.PeerUID != peerUID {
		b.deny(conn, EventSessionRevoked, req.SessionID, session.AgentName, "", peerUID, api.ErrCodeUIDMismatch, fmt.Sprintf("revoke denied: session owned by uid %d", session.PeerUID), "session is owned by a different user", session.RunID, session.TaskRef, start)
		return
	}
	if err := b.audit.Record(AuditEvent{Timestamp: time.Now(), EventType: EventSessionRevokeRequested, SessionID: req.SessionID, AgentName: session.AgentName, Repo: firstResourceIdentifier(session.Resources), PeerUID: peerUID, Success: true, DurationMS: time.Since(start).Milliseconds(), Metadata: correlationMetadata(session.RunID, session.TaskRef)}); err != nil {
		_ = b.store.Revoke(req.SessionID)
		b.writeAuditError(conn, err)
		return
	}
	if err := b.store.Revoke(req.SessionID); err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, err.Error())
		return
	}

	if err := b.audit.Record(AuditEvent{
		Timestamp:  time.Now(),
		EventType:  EventSessionRevoked,
		SessionID:  req.SessionID,
		AgentName:  session.AgentName,
		Repo:       firstResourceIdentifier(session.Resources),
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
		Metadata:   correlationMetadata(session.RunID, session.TaskRef),
	}); err != nil {
		b.writeAuditError(conn, err)
		return
	}

	b.writeSuccess(conn, &api.RevokeSessionResponse{Revoked: true})
}

func (b *Broker) handleSessionStatus(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req api.SessionStatusRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "invalid session_status body: "+err.Error())
		return
	}
	session, err := b.store.Get(req.SessionID)
	if err != nil {
		b.writeError(conn, api.ErrCodeSessionNotFound, err.Error())
		return
	}
	if session.PeerUID != peerUID {
		b.writeError(conn, api.ErrCodeUIDMismatch, "session is owned by a different user")
		return
	}

	resources := make([]string, 0, len(session.Resources))
	for _, r := range session.Resources {
		resources = append(resources, r.String())
	}
	b.writeSuccess(conn, &api.SessionStatusResponse{
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
	var req api.HealthCheckRequest
	if len(body) != 0 && string(body) != "null" {
		if err := json.Unmarshal(body, &req); err != nil {
			b.writeError(conn, api.ErrCodeBrokerUnavailable, "invalid health_check body: "+err.Error())
			return
		}
	}
	if err := b.audit.Health(); err != nil {
		b.writeAuditError(conn, err)
		return
	}
	b.writeSuccess(conn, &api.HealthCheckResponse{Healthy: true})
}

func (b *Broker) deny(conn net.Conn, eventType, sessionID, agentName, repo string, peerUID uint32, code, detail, message, runID, taskRef string, start time.Time) {
	err := b.audit.Record(AuditEvent{
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
	if err != nil {
		b.writeAuditError(conn, err)
		return
	}
	b.writeError(conn, code, message)
}

func (b *Broker) writeAuditError(conn net.Conn, err error) {
	b.writeError(conn, api.ErrCodeBrokerUnavailable, fmt.Sprintf("audit persistence failed: %v", err))
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
		b.writeError(conn, api.ErrCodeBrokerUnavailable, "marshal response: "+err.Error())
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(connWriteTimeout))
	_ = json.NewEncoder(conn).Encode(api.Response{OK: true, Body: bodyJSON})
}

func (b *Broker) writeError(conn net.Conn, code, message string) {
	if code != api.ErrCodeBrokerUnavailable {
		if err := b.audit.Health(); err != nil {
			code = api.ErrCodeBrokerUnavailable
			message = fmt.Sprintf("audit persistence failed: %v", err)
		}
	}
	_ = conn.SetWriteDeadline(time.Now().Add(connWriteTimeout))
	_ = json.NewEncoder(conn).Encode(api.Response{
		OK:    false,
		Error: &api.ErrorResponse{Code: code, Message: message},
	})
}

func (b *Broker) ReloadPolicy() error {
	var p *policy.PolicyFile
	var err error
	if b.config.IdentitiesPath == "" {
		p, err = policy.Load(b.config.PolicyPath)
	} else {
		var snapshot store.Snapshot
		snapshot, err = store.Load(b.config.IdentitiesPath, b.config.PolicyPath)
		if err == nil {
			p, err = snapshot.Policy, snapshot.PolicyError
		}
	}
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
