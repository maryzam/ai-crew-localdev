# ADR 0004: Brokered native agent telemetry

**Status:** Accepted

**Date:** 2026-06-29

## Context

Managed runs need request-level token evidence for local history, Langfuse, and future meta-agent analysis. The first implementation used ccusage as a local log adapter. That added a separate Node dependency, inferred deltas across runs, and duplicated native OpenTelemetry support already present in Claude Code and Codex.

Langfuse project keys are credentials. Passing them through container or agent environment variables would bypass the credential broker.

## Decision

Remove ccusage before release. Use native Claude and Codex OpenTelemetry for managed runs.

Add a `langfuse:project:<id>` broker resource and a telemetry-egress provider capability. The provider reads project keys from an absolute, owner-only regular file with `O_NOFOLLOW`. Policy binds the file, project, and endpoint to each allowed agent. Langfuse is not registered as a credential minter.

The launcher starts an authenticated relay on a random loopback port. The agent gets only a random publish token. The relay sends its sanitized projection through the authenticated broker session, and only the broker-side provider reads and uses backend keys. Ambient OpenTelemetry and Langfuse settings are scrubbed before governed values are added.

The relay accepts bounded OTLP/HTTP JSON. It extracts provider-reported request usage from native log events and rebuilds native trace payloads from an allowlist with run correlation. The broker validates the bounded projection again before the provider sends it to Langfuse. Prompt content, tool content, raw API bodies, unknown fields, and status messages are not forwarded. Metrics are disabled to avoid duplicate token accounting.

Local JSONL remains the durable run summary. Remote export failure does not grant access, expose credentials, or stop the agent. Usage fields record source, scope, precision, and confidence. Missing values remain empty.

## Consequences

- There is no ccusage install, command, log parser, or migration path to maintain.
- Native telemetry settings are version-sensitive and need contract tests when Claude or Codex versions change.
- Claude interactive traces form native trace roots. Run IDs are added as metadata because interactive Claude sessions ignore inbound trace context.
- Codex receives its relay token in process arguments because its supported one-run exporter configuration uses `-c`. The token can only publish to one loopback listener for that launcher lifetime.
- Langfuse setup now changes broker policy and therefore requires this security decision record.

## Out of scope

- Langfuse dashboards and scoring.
- Resource metrics.
- Meta-agent recommendations.
- Guaranteed cost fields for providers that do not emit cost.
