# ADR 0005: Durable governance evidence

**Status:** Accepted
**Date:** 2026-06-30

## Context

The broker previously queued audit records in memory and discarded records when the queue filled. Buffered writes ignored serialization, write, flush, and close failures, so a successful credential or session response did not prove that its audit evidence was durable. Governance configuration and session metadata used direct file replacement, allowing interruption, truncation, symlink traversal, permissive files, path traversal, and identities-policy generation mismatch.

## Decision

The broker records audit events synchronously under serialization, appends one complete JSON record, synchronizes the file, and returns the persistence result to the request path. A created audit file is owner-only, regular, opened without following symlinks, and made durable by synchronizing its parent directory. Audit failure becomes sticky health failure and successful broker responses are withheld. Credential mint, session revoke, and session expiry record durable intent before external or irreversible transitions. Revocation still proceeds when audit intent fails because leaving a credential session active is less safe; the request fails and broker health remains failed.

Governance files use same-directory temporary files, complete writes, file synchronization, atomic rename, and directory synchronization. Identity and policy publication writes an owner-only transaction journal before replacing either destination. Recovery rolls the committed journal forward. Readers and publishers share an owner-only file lock, so identities and policy are observed as one generation. Invalid, duplicate, oversized, incomplete, permissive, non-regular, and symlink-backed state is rejected.

Session metadata and other governance state use the same atomic owner-only primitive. Session identifiers are constrained before path construction. Security-sensitive reads are bounded, owner-only, regular, and no-follow.

## Consequences

- Broker latency includes durable audit persistence and must be measured on deployment filesystems.
- Audit storage availability is part of broker readiness and credential availability.
- Configuration publication can report a committed transaction that still requires recovery after a storage failure; guarded readers deterministically complete it before returning state.
- Existing permissive identity, policy, key, audit, or session files must be secured before the broker accepts them.
- Revocation can complete without audit evidence only when audit storage has already failed; this produces an error response and unhealthy broker rather than unaudited success.

## Out of scope

- Remote audit replication or an external write-ahead log.
- Multi-host configuration consensus.
- A fixed audit-latency budget before measurement on representative persistent filesystems.
