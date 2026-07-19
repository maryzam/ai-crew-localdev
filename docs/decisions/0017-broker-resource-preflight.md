# ADR 0017: Broker Resource Preflight

## Status

Accepted

## Context

Project manifest resources are repository-controlled declarations, while host governance policy is broker-controlled authority. Managed runs need to reject manifest resources that host policy does not grant before broker session creation, but a planner-side read of `AI_AGENT_POLICY_PATH` from inside the workspace would make a container-local file path part of the trust boundary and let repo-controlled execution context influence the precheck.

## Decision

Add a broker `authorize_resources` control method that accepts agent name, requested resource URIs, and optional run correlation metadata, then runs the same broker policy authorization path used by `create_session` without creating a session or returning credentials. The launcher calls this method before `create_session`; session creation still revalidates resources so policy reloads or races fail closed. Successful preflight writes durable broker audit evidence, and denial uses the broker's existing fail-closed error and audit path. The planner continues to validate manifest and provider resource grammar before producing a run plan, but it does not read host governance files from inside the managed workspace.

## Consequences

Manifest resource denials surface before session creation with broker-authoritative policy semantics, avoiding opaque late failures and avoiding duplicated policy logic in the control plane. The broker remains the only host governance authority, every privileged session request is still rechecked at creation, and the extra local broker round trip is accepted as part of the security boundary rather than treated as an implementation cost.
