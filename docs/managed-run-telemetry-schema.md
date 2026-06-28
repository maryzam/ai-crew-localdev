# Managed-Run Telemetry Schema

This document is generated from `internal/telemetry/schema.go`. Run `go run ./cmd/telemetry-schema` after changing the field registry.

## Budgets

- Schema version: `2.0`
- Root span attributes: at most 48
- Child span attributes: at most 24
- Span-event attributes: at most 12
- Propagated Langfuse metadata fields: at most 8
- Propagated metadata and session values: at most 200 characters
- Tags: at most 8 values of at most 64 characters

High-cardinality values are retained on traces but are never metric dimensions. Sensitive and unbounded values remain local-only.

## Field Registry

| Field | Scope | Destinations | Cardinality | Max length | Sensitive | Metric |
|---|---|---|---|---:|---|---|
| `ai_agent.schema.version` | trace | local, otlp, langfuse | low | 16 | false | true |
| `ai_agent.run.id` | trace | local, otlp, broker, environment | high | 64 | false | false |
| `ai_agent.run.mode` | trace | local, otlp, langfuse | low | 16 | false | true |
| `ai_agent.run.outcome` | trace | local, otlp, langfuse | low | 32 | false | true |
| `ai_agent.run.terminal_phase` | trace | local, otlp, langfuse | low | 32 | false | true |
| `ai_agent.run.signal` | trace | local, otlp | low | 32 | false | false |
| `ai_agent.task.ref` | trace | local, otlp, langfuse, broker, environment | high | 200 | false | false |
| `ai_agent.repository.slug` | trace | local, otlp, langfuse | workspace | 200 | false | false |
| `ai_agent.repository.commit` | trace | local, otlp | high | 64 | false | false |
| `ai_agent.repository.branch` | trace | local, otlp | high | 200 | false | false |
| `ai_agent.repository.dirty` | trace | local, otlp | low | - | false | true |
| `ai_agent.repository.root_path` | local | local | unbounded | 4096 | true | false |
| `ai_agent.agent.type` | trace | local, otlp, langfuse | low | 32 | false | true |
| `ai_agent.agent.identity` | trace | local, otlp | workspace | 128 | false | false |
| `ai_agent.agent.version` | trace | local, otlp | workspace | 64 | false | false |
| `gen_ai.provider.name` | trace | local, otlp, langfuse | low | 64 | false | true |
| `gen_ai.request.model` | trace | local, otlp, langfuse | high | 200 | false | false |
| `gen_ai.response.model` | trace | local, otlp, langfuse | high | 200 | false | false |
| `ai_agent.model.family` | trace | local, otlp, langfuse | low | 64 | false | true |
| `ai_agent.model.confidence` | trace | local, otlp, langfuse | low | 16 | false | true |
| `ai_agent.model.source` | trace | local, otlp | low | 32 | false | true |
| `ai_agent.broker.session.id` | trace | local, otlp, broker | high | 128 | false | false |
| `ai_agent.verify.enabled` | trace | local, otlp, langfuse | low | - | false | true |
| `ai_agent.attempt` | span | local, otlp | low | - | false | true |
| `ai_agent.exit_code` | span | local, otlp | low | - | false | true |
| `ai_agent.command.sha256` | span | local, otlp | high | 64 | false | false |
| `ai_agent.usage.status` | trace | local, otlp | low | 32 | false | true |
| `gen_ai.usage.input_tokens` | trace | local, otlp | high | - | false | false |
| `gen_ai.usage.output_tokens` | trace | local, otlp | high | - | false | false |
| `gen_ai.usage.cache_read.input_tokens` | trace | local, otlp | high | - | false | false |
| `gen_ai.usage.reasoning.output_tokens` | trace | local, otlp | high | - | false | false |
| `ai_agent.diagnostics.error_summary` | local | local | unbounded | 512 | true | false |
| `ai_agent.diagnostics.output_path` | local | local | unbounded | 4096 | true | false |

## Identity Semantics

- `run_id` identifies one managed invocation and maps to one trace.
- `broker.session_id` identifies the credential lease for that run.
- `task.ref` optionally groups multiple runs and maps to the Langfuse session ID.
- Requested and observed models remain separate; model family is the bounded aggregation dimension.
- Repository paths, diagnostic output, prompts, credentials, and complete commands are not exported.
