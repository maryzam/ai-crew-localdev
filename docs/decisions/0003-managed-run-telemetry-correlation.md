# ADR 0003: Managed-run telemetry correlation

**Status:** Accepted

**Date:** 2026-06-26

## Context

`ai-agent run` is the supported managed execution path. The broker already records JSONL audit events for credential and session activity, while Langfuse support previously only started infrastructure. The product needs durable run history for operators and future meta-agent analysis without moving workflow logic into the broker.

The broker is still the credential governance boundary. It should stay small, strict, and auditable. Telemetry should observe the runtime and correlate with audit records, not become a new authorization input.

## Decision

Generate a stable run ID in `ai-agent run` before creating a broker session. Pass that ID to the broker in `CreateSessionRequest.RunID`, store it on the session, and copy it into broker audit metadata for session and credential events.

`ai-agent run` owns managed-run telemetry. It writes append-only local JSONL events under a cross-process lock, rotates that history with one backup, and reconstructs canonical run summaries for `ai-agent runs list` and `ai-agent runs show`. Local history is the durable source of truth and remains useful without an observability backend.

Remote delivery uses OTLP/HTTP JSON. Langfuse is one supported OTLP backend, not the telemetry data model. Export happens with a bounded timeout at run close and reports one operator warning on failure. Broker JSONL audit remains the source of truth for auth events.

Run, broker session, and task identities remain separate. A run ID identifies one invocation and trace; a broker session ID identifies its credential lease; an optional task reference groups multiple runs and maps to a Langfuse session. Every managed launch that starts a recorder emits exactly one terminal event.

The versioned field registry in `internal/platform/telemetry/schema.go` owns propagation, cardinality, length, privacy, and metric-dimension policy. The generated `internal/platform/telemetry/schema.generated.md` reference and telemetry conformance tests prevent documentation and exporter mappings from drifting independently.

The launcher passes `AI_AGENT_RUN_ID` to the agent process for correlation and scrubs ambient Langfuse API keys from the child environment. Governed Langfuse keys are read and used only by the broker-side provider.

## Schema versioning and compatibility

The persisted local schema carries an explicit `schema_version` (`internal/platform/telemetry/schema.go`). History readers accept only events whose version matches the current `SchemaVersion` and skip anything else, including crash-truncated lines.

While the tool is pre-release with no external telemetry consumers, schema breaks are a deliberate clean cut: an incompatible bump (for example the `1.0` to `2.0` move that nested run-level state under `run`) drops older `~/.config/ai-agent/run-telemetry.jsonl` records from `ai-agent runs` rather than migrating them. Local history is dev-only and reproducible by running again, so migration and dual-version readers are explicit non-goals at this stage. Operators who want to keep pre-upgrade history archive the JSONL file themselves.

This clean-break policy is bounded to pre-release. Once dashboards, the meta-agent, or any durable consumer depends on this substrate, schema changes must become additive within a major version, and any breaking bump must ship a migration or a documented retention path. The single-source field registry and generated `internal/platform/telemetry/schema.generated.md` keep the schema, exporter mapping, and documentation from drifting independently in the meantime.

## Consequences

**Gains:**
- Operators can inspect recent runs without reading JSONL or running Langfuse.
- Operators get stable run and task identity across local telemetry, Langfuse, agent subprocesses, and broker audit records.
- The broker remains responsible for auth audit only; telemetry behavior stays in the runtime layer.
- Langfuse credentials do not become ambient secrets available to agents.
- The bundled local Langfuse UI binds to loopback because its generated bootstrap credentials are intended for one workstation.
- A slow or unavailable Langfuse endpoint does not block managed-run lifecycle transitions.

**Costs:**
- `CreateSessionRequest` gains an optional wire field. Older clients can omit it; newer audit events include correlation metadata when present.
- Unavailable token and cost values are omitted until agent-specific adapters can extract real usage.
- Model resolution preserves requested and observed values separately and falls back to useful provider or family grouping without inventing an exact model.
- Local run history keeps only the active JSONL file plus one rotated backup.
- Each event opens the local history under a short file lock so concurrent runs cannot keep writing to a stale descriptor after rotation.

## Out of scope

- Langfuse dashboards, scoring, or Git annotations.
- Agent-native hooks required to observe exact model and token usage from every supported agent.
- Resource-use metrics.
- Meta-agent analysis or automated workflow recommendations.
- Moving broker audit ingestion directly into Langfuse.
