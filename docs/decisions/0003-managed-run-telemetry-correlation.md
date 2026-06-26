# ADR 0003: Managed-run telemetry correlation

**Status:** Accepted
**Date:** 2026-06-26

## Context

`ai-agent run` is the supported managed execution path. The broker already
records JSONL audit events for credential and session activity, while Langfuse
support previously only started infrastructure. The product needs durable run
history for operators and future meta-agent analysis without moving workflow
logic into the broker.

The broker is still the credential governance boundary. It should stay small,
strict, and auditable. Telemetry should observe the runtime and correlate with
audit records, not become a new authorization input.

## Decision

Generate a stable run ID in `ai-agent run` before creating a broker session.
Pass that ID to the broker in `CreateSessionRequest.RunID`, store it on the
session, and copy it into broker audit metadata for session and credential
events.

`ai-agent run` owns managed-run telemetry. It writes local JSONL run history and
optionally mirrors trace and event data into Langfuse when Langfuse API keys are
configured. Broker JSONL audit remains the source of truth for auth events.

The launcher passes `AI_AGENT_RUN_ID` to the agent process for correlation, but
scrubs Langfuse API keys from the child environment after initializing
telemetry.

## Consequences

**Gains:**
- Operators get a stable run identity across local telemetry, Langfuse, agent
  subprocesses, and broker audit records.
- The broker remains responsible for auth audit only; telemetry behavior stays
  in the runtime layer.
- Langfuse credentials do not become ambient secrets available to agents.

**Costs:**
- `CreateSessionRequest` gains an optional wire field. Older clients can omit
  it; newer audit events include correlation metadata when present.
- Token and cost numbers are explicit `unknown` values until agent-specific
  adapters can extract real usage.

## Out of scope

- Langfuse dashboards, scoring, or Git annotations.
- Resource-use metrics.
- Meta-agent analysis or automated workflow recommendations.
- Moving broker audit ingestion directly into Langfuse.
