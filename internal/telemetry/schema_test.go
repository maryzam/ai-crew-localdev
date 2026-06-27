package telemetry

import (
	"strings"
	"testing"
)

func TestTelemetryFieldPoliciesConform(t *testing.T) {
	if err := ValidateFieldPolicies(); err != nil {
		t.Fatal(err)
	}
	for _, field := range FieldPolicies {
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
	attributes := propagatedAttributes(event)
	metadataCount := 0
	for _, raw := range attributes {
		attribute := raw.(map[string]any)
		key := attribute["key"].(string)
		if key == "langfuse.trace.tags" {
			values := attribute["value"].(map[string]any)["arrayValue"].(map[string]any)["values"].([]any)
			if len(values) > MaxTagCount {
				t.Errorf("tag count = %d, max %d", len(values), MaxTagCount)
			}
			for _, rawValue := range values {
				value := rawValue.(map[string]any)["stringValue"].(string)
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
			value := attribute["value"].(map[string]any)["stringValue"].(string)
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

func representativeEvent() Event {
	exitCode := 1
	return Event{
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
		SessionID:     "sess-123",
		Phase:         PhaseVerify,
		Attempt:       2,
		Outcome:       OutcomeVerifyFailed,
		ExitCode:      &exitCode,
		VerifyEnabled: true,
		MaxRetries:    2,
	}
}
