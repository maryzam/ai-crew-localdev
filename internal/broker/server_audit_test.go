package broker

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

type testAuditSink struct {
	err error
}

func (s *testAuditSink) Record(AuditEvent) error {
	return s.err
}

func (s *testAuditSink) Health() error {
	return s.err
}

type recordFailureAuditSink struct{}

func (recordFailureAuditSink) Record(AuditEvent) error {
	return errors.New("storage failed")
}

func (recordFailureAuditSink) Health() error {
	return nil
}

type orderedAuditSink struct {
	mu     sync.Mutex
	events []string
	failOn string
	err    error
}

func (s *orderedAuditSink) Record(event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.EventType == s.failOn {
		s.err = errors.New("storage failed")
		return s.err
	}
	s.events = append(s.events, event.EventType)
	return nil
}

func (s *orderedAuditSink) Health() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *orderedAuditSink) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func TestNewBrokerRequiresAuditSink(t *testing.T) {
	if _, err := NewBroker(BrokerConfig{}, nil, nil, nil); err == nil {
		t.Fatal("nil audit sink accepted")
	}
}

func TestBrokerFailsClosedWhenAuditUnavailable(t *testing.T) {
	b, socketPath, cleanup := testBroker(t)
	defer cleanup()
	b.audit = &testAuditSink{err: errors.New("storage failed")}
	body, err := json.Marshal(brokerapi.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"github:repo:owner/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	response := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodCreateSession, Body: body})
	if response.OK || response.Error == nil || response.Error.Code != brokerapi.ErrCodeBrokerUnavailable {
		t.Fatalf("create response = %#v", response)
	}
	health := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodHealthCheck})
	if health.OK || health.Error == nil || health.Error.Code != brokerapi.ErrCodeBrokerUnavailable {
		t.Fatalf("health response = %#v", health)
	}
}

func TestBrokerDenialUsesImmediateAuditFailure(t *testing.T) {
	b, socketPath, cleanup := testBroker(t)
	defer cleanup()
	b.audit = recordFailureAuditSink{}
	body, err := json.Marshal(brokerapi.CreateSessionRequest{AgentName: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	response := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodCreateSession, Body: body})
	if response.OK || response.Error == nil || response.Error.Code != brokerapi.ErrCodeBrokerUnavailable {
		t.Fatalf("response = %#v", response)
	}
}

func TestBrokerDoesNotMintWithoutDurableAuditIntent(t *testing.T) {
	b, socketPath, cleanup := testBroker(t)
	defer cleanup()
	createBody, err := json.Marshal(brokerapi.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"github:repo:owner/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	created := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodCreateSession, Body: createBody})
	if !created.OK {
		t.Fatalf("create response = %#v", created)
	}
	var session brokerapi.CreateSessionResponse
	if err := json.Unmarshal(created.Body, &session); err != nil {
		t.Fatal(err)
	}
	b.audit = &testAuditSink{err: errors.New("storage failed")}
	mintBody, err := json.Marshal(brokerapi.CredentialRequest{SessionID: session.SessionID, BindSecret: session.BindSecret, CredentialType: githubcontract.CredentialType, Resource: "github:repo:owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	response := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: mintBody})
	if response.OK || response.Error == nil || response.Error.Code != brokerapi.ErrCodeBrokerUnavailable {
		t.Fatalf("mint response = %#v", response)
	}
	stored, err := b.store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MintCount != 0 {
		t.Fatalf("mint count = %d", stored.MintCount)
	}
}

func TestBrokerRevocationRemainsFailClosedWhenAuditFails(t *testing.T) {
	b, socketPath, cleanup := testBroker(t)
	defer cleanup()
	createBody, err := json.Marshal(brokerapi.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"github:repo:owner/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	created := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodCreateSession, Body: createBody})
	var session brokerapi.CreateSessionResponse
	if err := json.Unmarshal(created.Body, &session); err != nil {
		t.Fatal(err)
	}
	b.audit = &testAuditSink{err: errors.New("storage failed")}
	revokeBody, err := json.Marshal(brokerapi.RevokeSessionRequest{SessionID: session.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	response := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodRevokeSession, Body: revokeBody})
	if response.OK || response.Error == nil || response.Error.Code != brokerapi.ErrCodeBrokerUnavailable {
		t.Fatalf("revoke response = %#v", response)
	}
	stored, err := b.store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Revoked {
		t.Fatal("session remained active")
	}
}

func TestBrokerRevocationRecordsIntentBeforeTransition(t *testing.T) {
	b, socketPath, cleanup := testBroker(t)
	defer cleanup()
	createBody, err := json.Marshal(brokerapi.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"github:repo:owner/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	created := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodCreateSession, Body: createBody})
	var session brokerapi.CreateSessionResponse
	if err := json.Unmarshal(created.Body, &session); err != nil {
		t.Fatal(err)
	}
	audit := &orderedAuditSink{}
	b.audit = audit
	revokeBody, err := json.Marshal(brokerapi.RevokeSessionRequest{SessionID: session.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	response := sendRequest(t, socketPath, brokerapi.Request{Method: brokerapi.MethodRevokeSession, Body: revokeBody})
	if !response.OK {
		t.Fatalf("response = %#v", response)
	}
	events := audit.recorded()
	if len(events) != 2 || events[0] != EventSessionRevokeRequested || events[1] != EventSessionRevoked {
		t.Fatalf("events = %v", events)
	}
}

func TestBrokerExpirationRequiresDurableIntent(t *testing.T) {
	newExpiredBroker := func(t *testing.T, audit AuditSink) (*Broker, string) {
		t.Helper()
		store := NewMemorySessionStore()
		store.SessionTTL = time.Nanosecond
		session, _, err := store.Create(brokerapi.CreateSessionRequest{AgentName: "agent", Resources: []string{"github:repo:owner/repo"}}, uint32(1000))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
		return &Broker{store: store, audit: audit}, session.ID
	}
	t.Run("failed intent retains session", func(t *testing.T) {
		audit := &orderedAuditSink{failOn: EventSessionExpireRequested}
		broker, sessionID := newExpiredBroker(t, audit)
		broker.cleanupSessions()
		if _, err := broker.store.Get(sessionID); err != nil {
			t.Fatalf("session removed without intent: %v", err)
		}
	})
	t.Run("successful intent precedes deletion", func(t *testing.T) {
		audit := &orderedAuditSink{}
		broker, sessionID := newExpiredBroker(t, audit)
		broker.cleanupSessions()
		if _, err := broker.store.Get(sessionID); err == nil {
			t.Fatal("expired session retained")
		}
		events := audit.recorded()
		if len(events) != 2 || events[0] != EventSessionExpireRequested || events[1] != EventSessionExpired {
			t.Fatalf("events = %v", events)
		}
	})
	t.Run("failed result retains intent and health failure", func(t *testing.T) {
		audit := &orderedAuditSink{failOn: EventSessionExpired}
		broker, sessionID := newExpiredBroker(t, audit)
		broker.cleanupSessions()
		if _, err := broker.store.Get(sessionID); err == nil {
			t.Fatal("expired session retained")
		}
		events := audit.recorded()
		if len(events) != 1 || events[0] != EventSessionExpireRequested || audit.Health() == nil {
			t.Fatalf("events=%v health=%v", events, audit.Health())
		}
	})
}
