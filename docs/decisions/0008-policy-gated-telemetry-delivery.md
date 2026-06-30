# ADR 0008: Policy-Gated Telemetry Delivery

## Status

Accepted

## Context

Managed-run OTLP projection, native-agent ingestion, and local recording evolved independently. Native ingestion had a second string allowlist, managed OTLP payloads were nested untyped maps, and disabled telemetry was represented by a nil recorder. An initial refactoring also added runtime delivery counters with no operator-facing consumer; those counters increased complexity without making the system observable.

## Decision

The private telemetry field registry is the authority for field identity, retention destinations, sensitivity, cardinality, value length, metric eligibility, and native-ingestion eligibility. Managed and native OTLP paths reject fields that lack an explicit non-sensitive OTLP policy. Native aliases resolve to canonical field identifiers before values are bounded and encoded. Transport-specific correlation attributes are derived only from approved source fields.

Managed OTLP encoding uses typed wire DTOs. A non-nil disabled recorder implements inert behavior when telemetry is disabled. Queue limits, payload limits, timeouts, terminal-event preservation, and one-shot operator warnings are executable transport behavior. Local sink throughput has a benchmark; any future buffering or timeout change must add a benchmark at the affected transport boundary.

## Consequences

Telemetry call sites no longer branch around nil recorders. Adding a native attribute or managed export requires a registry policy and boundary validation. Sensitive local fields cannot become exportable through a second allowlist. Saturation and export failure remain visible to operators, while unused measurement machinery is absent. Typed DTOs increase the explicit wire surface but remove runtime shape ambiguity.
