package broker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
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
	signer    *Signer
	store     *MemorySessionStore
	cache     *MemoryTokenCache
	audit     *FileAuditLogger
	limiter   *RateLimiter
	enforcer  *PolicyEnforcer
	github    *GitHubClient
	idents    *identity.IdentitiesFile
	config    BrokerConfig
	myUID     uint32
	providers map[string]CredentialProvider
}

// NewBroker constructs a broker from the given configuration, identity,
// and policy files.
func NewBroker(
	cfg BrokerConfig,
	idents *identity.IdentitiesFile,
	enforcer *PolicyEnforcer,
	signer *Signer,
	audit *FileAuditLogger,
) *Broker {
	store := NewMemorySessionStore()
	if cfg.SessionTTL > 0 {
		store.SessionTTL = cfg.SessionTTL
	}
	if cfg.IdleTimeout > 0 {
		store.IdleTimeout = cfg.IdleTimeout
	}

	return &Broker{
		signer:    signer,
		store:     store,
		cache:     NewMemoryTokenCache(cfg.CacheTTL),
		audit:     audit,
		limiter:   NewRateLimiter(cfg.SessionRateLimit, cfg.RepoRateLimit),
		enforcer:  enforcer,
		github:    NewGitHubClient(cfg.GitHubBaseURL),
		idents:    idents,
		config:    cfg,
		myUID:     uint32(os.Getuid()),
		providers: map[string]CredentialProvider{},
	}
}

// RegisterProvider installs a CredentialProvider for the given
// credential_type. Constructed providers are registered by main (and by
// tests) after NewBroker returns so that provider packages can depend on
// broker types without creating an import cycle.
func (b *Broker) RegisterProvider(p CredentialProvider) {
	b.providers[p.Type()] = p
}

// GitHubClient returns the broker's GitHub HTTP client. Exposed so that
// the provider wiring in main can construct a GitHub CredentialProvider
// without re-creating the client (which is configured from BrokerConfig).
func (b *Broker) GitHubClient() *GitHubClient {
	return b.github
}

// Signer returns the broker's JWT signer. Exposed for provider wiring;
// see GitHubClient.
func (b *Broker) Signer() *Signer {
	return b.signer
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

// ---- Method handlers -------------------------------------------------------

// handleMintCredential is the credential-generic mint path. It looks up
// a CredentialProvider by req.CredentialType, authorizes the request
// against the session's resource list and policy, and dispatches to the
// provider for the actual mint. The cache key is generic
// (credential_type + resource + params hash); providers contribute the
// params hash to keep different parameter sets in distinct entries.
func (b *Broker) handleMintCredential(conn net.Conn, body json.RawMessage, peerUID uint32, start time.Time) {
	var req CredentialRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "invalid mint_credential body: "+err.Error())
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
		b.auditDenial(EventBindingFailed, req.SessionID, session.AgentName, "", peerUID, ErrCodeBindingMismatch, err.Error(), start)
		b.writeError(conn, ErrCodeBindingMismatch, "binding validation failed")
		return
	}

	// Check session is active.
	if !session.IsActive() {
		code := ErrCodeSessionExpired
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, code, "session inactive", start)
		b.writeError(conn, code, "session is no longer active")
		return
	}

	// Look up provider by credential_type.
	provider, ok := b.providers[req.CredentialType]
	if !ok {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeUnknownCredType, "credential_type="+req.CredentialType, start)
		b.writeError(conn, ErrCodeUnknownCredType, "unknown credential_type: "+req.CredentialType)
		return
	}

	// Parse the requested resource URI.
	resource, err := ParseResourceURI(req.Resource)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, "", peerUID, ErrCodeInvalidResourceURI, err.Error(), start)
		b.writeError(conn, ErrCodeInvalidResourceURI, err.Error())
		return
	}

	// Verify the parsed resource is a member of the session's resources.
	if !resourceInSession(resource, session.Resources) {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeResourceNotAllowed, "resource not in session", start)
		b.writeError(conn, ErrCodeResourceNotAllowed,
			fmt.Sprintf("resource %q is not bound to this session", resource.String()))
		return
	}

	// Rate-limit using the resource URI as the key.
	if !b.limiter.Allow(req.SessionID, resource.String()) {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeRateLimited, "rate limit exceeded", start)
		b.writeError(conn, ErrCodeRateLimited, "rate limit exceeded")
		return
	}

	// Resource-level authorization (URI match against current policy).
	if err := b.enforcer.AuthorizeResource(session.AgentName, resource); err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeResourceNotAllowed, err.Error(), start)
		b.writeError(conn, ErrCodeResourceNotAllowed, err.Error())
		return
	}

	// Resolve per-agent provider configuration for the credential type.
	providerCfg, code, err := b.providerConfigForAgent(session.AgentName, req.CredentialType)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, code, err.Error(), start)
		b.writeError(conn, code, err.Error())
		return
	}

	// Compute the provider's cache key contribution.
	paramsHash, err := credentialParamsHash(req.CredentialType, req.Params, providerCfg)
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodePermissionDenied, err.Error(), start)
		b.writeError(conn, ErrCodePermissionDenied, err.Error())
		return
	}

	cacheKey := CacheKey{
		CredentialType: req.CredentialType,
		Resource:       resource.String(),
		ParamsHash:     paramsHash,
	}

	cached, cacheHit, err := b.cache.GetOrFetch(cacheKey, func() (*CachedToken, error) {
		result, err := provider.Mint(context.Background(), ProviderMintRequest{
			Resource:       resource,
			Params:         req.Params,
			Agent:          session.AgentName,
			ProviderConfig: providerCfg,
		})
		if err != nil {
			return nil, err
		}

		// Pull a Token string out of the provider's credential payload so
		// the cache can hold it. The wire response still uses the raw
		// payload (re-marshalled below) — this is purely a cache shape
		// constraint and will be cleaned up when CachedToken is generalized.
		var tokenStr string
		switch req.CredentialType {
		case CredentialTypeGitHubAppInstallation:
			var ghc GitHubAppInstallationCredential
			if err := json.Unmarshal(result.Credential, &ghc); err != nil {
				return nil, fmt.Errorf("decode github credential: %w", err)
			}
			tokenStr = ghc.Token
		default:
			tokenStr = string(result.Credential)
		}
		return &CachedToken{
			Token:     tokenStr,
			ExpiresAt: result.ExpiresAt,
			CachedAt:  time.Now(),
		}, nil
	})
	if err != nil {
		b.auditDenial(EventTokenDenied, req.SessionID, session.AgentName, resource.Identifier, peerUID, ErrCodeUpstreamError, err.Error(), start)
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
		Repo:       resource.Identifier,
		PeerUID:    peerUID,
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
	})

	credPayload, err := marshalCredential(req.CredentialType, cached.Token)
	if err != nil {
		b.writeError(conn, ErrCodeBrokerUnavailable, "marshal credential: "+err.Error())
		return
	}

	b.writeSuccess(conn, &CredentialResponse{
		CredentialType: req.CredentialType,
		Resource:       resource.String(),
		Credential:     credPayload,
		ExpiresAt:      cached.ExpiresAt,
	})
}

// providerConfigForAgent builds the provider-specific config blob for the
// given agent and credential type from the currently loaded policy.
// Returns the config, a wire error code suitable for failure responses,
// and an error if no config is available.
func (b *Broker) providerConfigForAgent(agentName, credType string) (any, string, error) {
	switch credType {
	case CredentialTypeGitHubAppInstallation:
		ghCfg, err := b.enforcer.GitHubConfig(agentName)
		if err != nil {
			return nil, ErrCodeBrokerUnavailable, err
		}
		if ghCfg.InstallationID <= 0 {
			return nil, ErrCodeBrokerUnavailable, fmt.Errorf("agent %q has no installation_id configured", agentName)
		}
		appID := ghCfg.AppID
		if appID == "" {
			appID = b.appIDForAgent(agentName)
		}
		if appID == "" {
			return nil, ErrCodeBrokerUnavailable, fmt.Errorf("no app ID configured for agent %q", agentName)
		}
		return GitHubProviderConfig{
			InstallationID:     ghCfg.InstallationID,
			AppID:              appID,
			DefaultPermissions: ghCfg.DefaultPermissions,
		}, "", nil
	default:
		return nil, ErrCodeUnknownCredType, fmt.Errorf("no provider config builder for credential_type %q", credType)
	}
}

// credentialParamsHash returns the provider-specific stable hash for the
// params blob, used as the cache key contribution. Unknown credential
// types yield the empty string (no cache differentiation).
func credentialParamsHash(credType string, params json.RawMessage, providerCfg any) (string, error) {
	switch credType {
	case CredentialTypeGitHubAppInstallation:
		cfg, ok := providerCfg.(GitHubProviderConfig)
		if !ok {
			return "", fmt.Errorf("unexpected provider config type %T", providerCfg)
		}
		return gitHubParamsHash(params, cfg.DefaultPermissions), nil
	default:
		return "", nil
	}
}

// gitHubParamsHash computes a stable hash over the effective GitHub
// permissions (params override session defaults). It mirrors the logic
// in the github provider package; duplicating the small helper here
// keeps the broker free of an import cycle with that package.
func gitHubParamsHash(rawParams json.RawMessage, defaults map[string]string) string {
	perms := defaults
	if len(rawParams) > 0 && string(rawParams) != "null" {
		var p GitHubAppInstallationParams
		if err := json.Unmarshal(rawParams, &p); err == nil && len(p.Permissions) > 0 {
			perms = p.Permissions
		}
	}
	if len(perms) == 0 {
		return ""
	}
	keys := make([]string, 0, len(perms))
	for k := range perms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + perms[k]
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])
}

// marshalCredential reconstructs the on-wire credential payload from the
// flattened cache value. Today this just wraps a token string per type;
// when the cache is generalized to hold the raw provider payload this
// helper goes away.
func marshalCredential(credType, token string) (json.RawMessage, error) {
	switch credType {
	case CredentialTypeGitHubAppInstallation:
		return json.Marshal(GitHubAppInstallationCredential{Token: token})
	default:
		return json.RawMessage(token), nil
	}
}

// resourceInSession reports whether r is present in the session's bound
// resource set (full URI equality).
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

	// Policy check: every requested resource must be allowed for the agent.
	for _, raw := range req.Resources {
		parsed, err := ParseResourceURI(raw)
		if err != nil {
			b.auditDenial(EventTokenDenied, "", req.AgentName, raw, peerUID, ErrCodeInvalidResourceURI, err.Error(), start)
			b.writeError(conn, ErrCodeInvalidResourceURI, err.Error())
			return
		}
		if err := b.enforcer.AuthorizeResource(req.AgentName, parsed); err != nil {
			b.auditDenial(EventTokenDenied, "", req.AgentName, parsed.Identifier, peerUID, ErrCodeResourceNotAllowed, err.Error(), start)
			b.writeError(conn, ErrCodeResourceNotAllowed, err.Error())
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

// firstResourceIdentifier returns the identifier of the first resource in
// the slice (empty string if none). The audit log's Repo field is kept
// for backward-compatibility with downstream consumers and reflects the
// session's primary resource.
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

	// session_status is read-only; it must NOT advance LastActivity.
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
