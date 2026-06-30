package telemetry

import (
	"fmt"
	"strings"
	"testing"
)

func TestTelemetryFieldPoliciesConform(t *testing.T) {
	if err := validateFieldPolicies(); err != nil {
		t.Fatal(err)
	}
	for _, field := range fieldRegistry {
		if field.Cardinality == "" {
			t.Errorf("field %s lacks cardinality classification", field.Key)
		}
		if field.Sensitive {
			for _, destination := range field.Destinations {
				if destination != "local" {
					t.Errorf("sensitive field %s exported to %s", field.Key, destination)
				}
			}
		}
		if field.Metric && field.Cardinality != CardinalityLow {
			t.Errorf("metric field %s has %s cardinality", field.Key, field.Cardinality)
		}
	}
}

func TestLangfuseAttributeBudgets(t *testing.T) {
	event := representativeEvent()
	attributes := langfuseTraceAttributes(event)
	for _, attribute := range attributes {
		if attribute.Key == "langfuse.trace.tags" {
			values := attribute.Value.ArrayValue.Values
			if len(values) > MaxTagCount {
				t.Errorf("tag count = %d, max %d", len(values), MaxTagCount)
			}
			for _, rawValue := range values {
				value := *rawValue.StringValue
				if len(value) > MaxTagLength {
					t.Errorf("tag %q exceeds %d characters", value, MaxTagLength)
				}
			}
		}
	}
	if got := len(rootSpanAttributes(event)); got > MaxRootAttributes {
		t.Fatalf("root attributes = %d, max %d", got, MaxRootAttributes)
	}
	if got := len(childSpanAttributes(event)); got > MaxChildAttributes {
		t.Fatalf("child attributes = %d, max %d", got, MaxChildAttributes)
	}
}

func TestRunOutcomeStaysRootOnlyAndAttemptOutcomeIsPerSpan(t *testing.T) {
	event := representativeEvent()
	event.Outcome = OutcomePassed
	event.Run.Outcome = OutcomeVerifyFailed

	rootRunOutcome := attributeValue(rootSpanAttributes(event), "ai_agent.run.outcome")
	if rootRunOutcome != OutcomeVerifyFailed {
		t.Errorf("root ai_agent.run.outcome = %q, want %q", rootRunOutcome, OutcomeVerifyFailed)
	}
	if got := attributeValue(childSpanAttributes(event), "ai_agent.run.outcome"); got != "" {
		t.Errorf("child span must not carry ai_agent.run.outcome, got %q", got)
	}
	if got := attributeValue(childSpanAttributes(event), "ai_agent.attempt.outcome"); got != OutcomePassed {
		t.Errorf("child ai_agent.attempt.outcome = %q, want %q", got, OutcomePassed)
	}
}

func TestAuthorizedAttributeFromSensitiveSourceIsRejected(t *testing.T) {
	original := langfuseAttributesPolicy
	t.Cleanup(func() { langfuseAttributesPolicy = original })
	langfuseAttributesPolicy = append(append([]authorizedAttribute(nil), original...), authorizedAttribute{
		key:     "langfuse.trace.metadata.errorsummary",
		source:  "ai_agent.diagnostics.error_summary",
		extract: func(e Event) any { return e.Run.Diagnostics.ErrorSummary },
	})
	event := representativeEvent()
	event.Run.Diagnostics.ErrorSummary = "secret"
	if got := attributeValue(langfuseTraceAttributes(event), "langfuse.trace.metadata.errorsummary"); got != "" {
		t.Fatal("sensitive source was exported")
	}
}

func TestNoSensitiveValueCrossesOTLPBoundary(t *testing.T) {
	const secret = "SENSITIVE-DO-NOT-EXPORT"
	event := representativeEvent()
	event.Run.Repository.RootPath = secret
	event.Run.Diagnostics.ErrorType = secret
	event.Run.Diagnostics.ErrorSummary = secret
	event.Run.Diagnostics.OutputPath = secret

	surfaces := [][]otlpWireAttribute{
		rootSpanAttributes(event),
		childSpanAttributes(event),
		resourceAttributes(event),
	}
	for _, marker := range rootSpanEvents([]Event{event}) {
		surfaces = append(surfaces, marker.Attributes)
	}
	for _, attributes := range surfaces {
		for _, raw := range attributes {
			if rendered := fmt.Sprint(raw); strings.Contains(rendered, secret) {
				t.Errorf("sensitive value leaked into OTLP export: %s", rendered)
			}
		}
	}
}

func attributeValue(attributes []otlpWireAttribute, key string) string {
	for _, attribute := range attributes {
		if attribute.Key != key {
			continue
		}
		if attribute.Value.StringValue != nil {
			return *attribute.Value.StringValue
		}
	}
	return ""
}

func representativeEvent() Event {
	exitCode := 1
	inputTokens := int64(100)
	outputTokens := int64(25)
	return Event{
		SchemaVersion: SchemaVersion,
		Phase:         PhaseVerify,
		Attempt:       2,
		Outcome:       OutcomeVerifyFailed,
		ExitCode:      &exitCode,
		Metadata:      map[string]string{"command_sha256": strings.Repeat("b", 64)},
		Run: RunSummary{
			SchemaVersion: SchemaVersion,
			RunID:         "run_123",
			TraceID:       traceIDForRun("run_123"),
			Task:          TaskMetadata{Type: "github_issue", Ref: "github:owner/repo#43"},
			Repository:    RepositoryMetadata{Slug: "owner/repo", CommitSHA: strings.Repeat("a", 40), Branch: "feature/telemetry", Dirty: true},
			Agent:         AgentMetadata{Type: "codex", Identity: "codex-reviewer", Command: "codex"},
			Model: ModelAttribution{
				Provider: "openai", Family: "gpt-5", Requested: "gpt-5", Observed: "gpt-5.2-codex",
				Resolution: ModelResolution{Status: "resolved", Confidence: "observed", PrimarySource: "agent_telemetry", Sources: []string{"cli", "agent_telemetry"}, Conflict: true},
			},
			Outcome:       OutcomeVerifyFailed,
			TerminalPhase: PhaseVerify,
			ExitCode:      &exitCode,
			Execution:     ExecutionSummary{VerifyEnabled: true, MaxRetries: 2},
			Broker:        BrokerSummary{SessionID: "sess-123", SessionCreated: true},
			Usage:         &Usage{Status: "observed", InputTokens: &inputTokens, OutputTokens: &outputTokens},
		},
	}
}
