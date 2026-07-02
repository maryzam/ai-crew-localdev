# ADR 0010: Broker-Owned Provider Egress

## Status

Accepted

## Context

The Langfuse provider previously returned the durable project public and secret keys through the broker credential API. The trusted launcher used those keys for host-side OTLP export while agents received only a scoped loopback relay token. This protected the workspace boundary but allowed a durable provider secret to cross the broker process boundary, unlike the GitHub provider's broker-owned signing key.

## Decision

Provider registration distinguishes resource configuration, credential minting, and telemetry egress capabilities. Langfuse implements telemetry egress only and is not registered as a credential minter. The launcher projects and locally records native telemetry, then publishes bounded OTLP through its authenticated broker session. The broker reauthorizes the session resource, enforces an independent delivery rate budget and a three-second request deadline, persists intent evidence containing payload size and SHA-256 before egress, and invokes the Langfuse provider. The provider revalidates the typed OTLP projection, reads the owner-only credential file with `O_NOFOLLOW`, rejects redirects, and applies the durable keys only to the upstream request.

Telemetry transport accepts at most 1 MiB per payload, 120 deliveries per session per minute, and 240 deliveries per resource per minute. Invalid, unauthorized, expired, over-budget, unaudited, or upstream-failed requests fail deterministically without returning provider keys or endpoint configuration. Local managed-run history remains available when optional remote telemetry fails.

## Consequences

The broker API carries a session-authenticated telemetry payload rather than a Langfuse credential. The generic telemetry package exposes only an exporter port and cannot construct secret-bearing HTTP transports. Terminal remote telemetry is flushed before session revocation, while revocation results and failures remain durable local events. Focused contract, provider, broker, projection, lifecycle, audit, and launcher tests enforce the boundary and its budgets.
