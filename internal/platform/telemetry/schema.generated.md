# Managed-Run Telemetry Schema

This document is generated from `internal/platform/telemetry/schema.go`. Run `go run ./cmd/telemetry-schema` after changing the field registry.

## Budgets

- Schema version: `2.0`
- Root span attributes: at most 48
- Child span attributes: at most 24
- Span-event attributes: at most 12
- Propagated metadata and session values: at most 200 characters
- Tags: at most 8 values of at most 64 characters
- OTLP export payload: at most 1048576 bytes
- OTLP export structure: at most 8 resource spans, 8 scope spans, 128 spans, and 32 events per span

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
| `ai_agent.attempt.outcome` | span | local, otlp | low | 32 | false | true |
| `ai_agent.exit_code` | span | local, otlp | low | - | false | true |
| `ai_agent.command.sha256` | span | local, otlp | high | 64 | false | false |
| `ai_agent.usage.status` | trace | local, otlp | low | 32 | false | true |
| `ai_agent.usage.source` | trace | local, otlp | low | 32 | false | true |
| `ai_agent.usage.scope` | trace | local, otlp | low | 16 | false | true |
| `ai_agent.usage.precision` | trace | local, otlp | low | 16 | false | true |
| `ai_agent.usage.confidence` | trace | local, otlp | low | 32 | false | true |
| `gen_ai.usage.input_tokens` | trace | local, otlp | high | - | false | false |
| `gen_ai.usage.output_tokens` | trace | local, otlp | high | - | false | false |
| `gen_ai.usage.cache_read.input_tokens` | trace | local, otlp | high | - | false | false |
| `ai_agent.usage.cache_write.input_tokens` | trace | local, otlp | high | - | false | false |
| `gen_ai.usage.reasoning.output_tokens` | trace | local, otlp | high | - | false | false |
| `gen_ai.usage.total_tokens` | trace | local, otlp, langfuse | high | - | false | false |
| `ai_agent.usage.cost.amount` | trace | local, otlp, langfuse | high | - | false | false |
| `ai_agent.usage.cost.currency` | trace | local, otlp, langfuse | low | 8 | false | false |
| `ai_agent.diagnostics.error_summary` | local | local | unbounded | 512 | true | false |
| `ai_agent.diagnostics.output_path` | local | local | unbounded | 4096 | true | false |
| `gen_ai.system` | native | otlp | low | 64 | false | false |
| `gen_ai.response.id` | native | otlp | high | 200 | false | false |
| `service.name` | native | otlp | workspace | 128 | false | false |
| `service.namespace` | native | otlp | workspace | 128 | false | false |
| `service.version` | native | otlp | workspace | 64 | false | false |
| `telemetry.sdk.language` | native | otlp | low | 32 | false | false |
| `telemetry.sdk.name` | native | otlp | low | 64 | false | false |
| `telemetry.sdk.version` | native | otlp | low | 64 | false | false |
| `span.type` | native | otlp | low | 64 | false | false |
| `query_source` | native | otlp | low | 64 | false | false |
| `duration_ms` | native | otlp | high | - | false | false |
| `ttft_ms` | native | otlp | high | - | false | false |
| `attempt` | native | otlp | low | - | false | false |
| `success` | native | otlp | low | - | false | false |
| `status_code` | native | otlp | low | - | false | false |
| `stop_reason` | native | otlp | low | 64 | false | false |
| `response.has_tool_call` | native | otlp | low | - | false | false |
| `tool_name` | native | otlp | high | 128 | false | false |
| `result_tokens` | native | otlp | high | - | false | false |
| `decision` | native | otlp | low | 64 | false | false |
| `source` | native | otlp | low | 64 | false | false |
| `interaction.sequence` | native | otlp | high | - | false | false |
| `interaction.duration_ms` | native | otlp | high | - | false | false |
| `event.name` | native | otlp | low | 128 | false | false |
| `event.kind` | native | otlp | low | 128 | false | false |
| `cost_usd` | native | otlp | high | - | false | false |
| `user_prompt_length` | native | otlp | high | - | false | false |
| `prompt_length` | native | otlp | high | - | false | false |
| `tool_input_size_bytes` | native | otlp | high | - | false | false |
| `tool_result_size_bytes` | native | otlp | high | - | false | false |
| `error_type` | native | otlp | low | 128 | false | false |
| `speed` | native | otlp | low | 32 | false | false |
| `effort` | native | otlp | low | 32 | false | false |

## Versioning and Compatibility

History readers accept only events matching the current schema version and skip anything else, including crash-truncated lines. While the tool is pre-release with no durable consumers, an incompatible version bump is a deliberate clean break: older local records drop out of `ai-agent runs` rather than being migrated. Once a dashboard or meta-agent depends on this substrate, changes become additive within a major version and any breaking bump must ship a migration. See ADR 0003.

## Identity Semantics

- `run_id` identifies one managed invocation and maps to one trace.
- `broker.session_id` identifies the credential lease for that run.
- `task.ref` optionally groups multiple runs and maps to the Langfuse session ID.
- Requested and observed models remain separate; model family is the bounded aggregation dimension.
- Repository paths, diagnostic output, prompts, credentials, and complete commands are not exported.
