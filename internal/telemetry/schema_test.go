package telemetry

import (
	"fmt"
	"strings"
	"testing"
)

func TestTelemetryFieldPoliciesConform(t *testing.T) {
	if err := ValidateFieldPolicies(); err != nil {
		t.Fatal(err)
	}
	for _, field := range fieldPolicies() {
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

func TestTaskReferenceLangfuseBoundary(t *testing.T) {
	valid := strings.Repeat("a", MaxSessionIDLength)
	if err := ValidateTaskRef(valid); err != nil {
		t.Fatalf("maximum valid task ref rejected: %v", err)
	}
	if err := ValidateTaskRef(valid + "a"); err == nil {
		t.Fatal("overlong task ref accepted")
	}
	for _, invalid := range []string{"github:owner/repo #43", "github:owner/repo\n#43", "github:owner/répo#43"} {
		if err := ValidateTaskRef(invalid); err == nil {
			t.Errorf("invalid task ref %q accepted", invalid)
		}
	}
}

func TestLangfuseMetadataKeysAndBudgets(t *testing.T) {
	event := representativeEvent()
	attributes := langfuseTraceAttributes(event)
	metadataCount := 0
	for _, attribute := range attributes {
		key := attribute.Key
		if key == "langfuse.trace.tags" {
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
		if strings.HasPrefix(key, "langfuse.trace.metadata.") {
			metadataCount++
			leaf := strings.TrimPrefix(key, "langfuse.trace.metadata.")
			if !validMetadataKey(leaf) {
				t.Errorf("invalid propagated metadata key %q", leaf)
			}
			value := *attribute.Value.StringValue
			if len(value) > MaxPropagatedValueLength {
				t.Errorf("metadata value %q exceeds %d characters", leaf, MaxPropagatedValueLength)
			}
		}
	}
	if metadataCount > MaxPropagatedMetadata {
		t.Fatalf("propagated metadata count = %d, max %d", metadataCount, MaxPropagatedMetadata)
	}
	if got := len(rootSpanAttributes(event)); got > MaxRootAttributes {
		t.Fatalf("root attributes = %d, max %d", got, MaxRootAttributes)
	}
	if got := len(childSpanAttributes(event)); got > MaxChildAttributes {
		t.Fatalf("child attributes = %d, max %d", got, MaxChildAttributes)
	}
}

func TestEveryExportedAIAgentAttributeHasPolicy(t *testing.T) {
	event := representativeEvent()
	attributeSets := [][]otlpWireAttribute{rootSpanAttributes(event), childSpanAttributes(event)}
	for _, attributes := range attributeSets {
		for _, attribute := range attributes {
			key := attribute.Key
			if !strings.HasPrefix(key, "ai_agent.") && !strings.HasPrefix(key, "gen_ai.") {
				continue
			}
			policy, ok := fieldPolicy(key)
			if !ok {
				t.Errorf("exported attribute %q has no field policy", key)
				continue
			}
			if !slicesContains(policy.Destinations, "otlp") {
				t.Errorf("exported attribute %q policy does not allow OTLP", key)
			}
		}
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

func TestStaticExportsStayWithinBoundary(t *testing.T) {
	if err := validateStaticExports(); err != nil {
		t.Fatal(err)
	}
	if err := validateEventProjection(); err != nil {
		t.Fatal(err)
	}
}

func TestStaticExportFromSensitiveSourceIsRejected(t *testing.T) {
	original := langfuseHints
	t.Cleanup(func() { langfuseHints = original })
	langfuseHints = append(append([]staticAttr(nil), original...), staticAttr{
		key:         "langfuse.trace.metadata.errorsummary",
		destination: destOTLP,
		source:      "ai_agent.diagnostics.error_summary",
		extract:     func(e Event) any { return e.Run.Diagnostics.ErrorSummary },
	})
	if err := validateStaticExports(); err == nil {
		t.Fatal("static export deriving from a sensitive source must be rejected")
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

func TestLocalRunMetadataRespectsFieldLengthPolicies(t *testing.T) {
	agent, _ := ResolveAgentModel(strings.Repeat("a", 512), []string{"custom-agent"})
	repository := InspectRepository(strings.Repeat("/private-path", 512), strings.Repeat("r", 512))
	for key, value := range map[string]string{
		"ai_agent.agent.identity":       agent.Identity,
		"ai_agent.repository.slug":      repository.Slug,
		"ai_agent.repository.root_path": repository.RootPath,
	} {
		policy, _ := fieldPolicy(key)
		if len(value) > policy.MaxLength {
			t.Errorf("%s length = %d, max %d", key, len(value), policy.MaxLength)
		}
	}
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
