# ADR 0008: Policy-Gated Telemetry Delivery

## Status

Accepted

## Context

Managed-run OTLP projection, native-agent ingestion, and local recording evolved independently. Native ingestion had a second string allowlist, managed OTLP payloads were nested untyped maps, disabled telemetry was represented by a nil recorder, and delivery tradeoffs had no queryable measurements or budgets.

## Decision

The private telemetry field registry is the authority for field identity, retention destinations, sensitivity, cardinality, value length, metric eligibility, and native-ingestion eligibility. Managed and native OTLP paths reject fields that lack an explicit non-sensitive OTLP policy. Native aliases resolve to canonical field identifiers before values are bounded and encoded. Transport-specific correlation attributes are derived only from approved source fields.

Managed OTLP encoding uses typed wire DTOs while preserving the existing JSON contract. A non-nil disabled recorder implements inert behavior when telemetry is disabled. Local persistence, managed OTLP delivery, and native OTLP delivery share delivery measurements for payload size, queue depth and saturation, dropped and rejected events, export latency, and local-write latency. Default budgets remain observational until delivery behavior is changed by a separate measured decision.

## Consequences

Telemetry call sites no longer branch around nil recorders. Adding a native attribute or managed export requires a registry policy and boundary validation. Sensitive local fields cannot become exportable through a second allowlist. Delivery regressions are visible through recorder statistics and deterministic budget counters. Typed DTOs increase the explicit wire surface but remove runtime shape ambiguity.
