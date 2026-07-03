# ADR 0011: Local Adaptive Optimization

## Status

Accepted

## Context

Native request usage was collected only when a Langfuse resource enabled remote trace export. That made local optimization evidence depend on optional infrastructure. Retained history exposed individual runs but did not turn cross-project outcomes, retries, token use, or verification coverage into actionable guidance.

Authentication mode is personal agent state and must not become telemetry metadata. Claude and Codex emit the same native usage event contracts after authentication, so coverage should be enforced at the launcher and event-normalization boundaries without recording whether a user selected stored OAuth, ChatGPT sign-in, or an API key.

## Decision

Start the authenticated native telemetry relay for every managed Claude and Codex run when local telemetry is enabled. Extract normalized request usage into local history independently of remote export. Configure an exporter and forward sanitized traces only when the broker session includes an authorized observability resource. Preserve source, scope, precision, and confidence, and leave unsupported cost empty.

Add `internal/app/adaptive` as a read-only application service above the telemetry store. `ai-agent runs analyze` supplies retained cross-project summaries and renders either human-readable or versioned JSON output. The service emits coverage and provider-reported cost totals, then recommends action for recurring failures, retry waste, high-token runs, successful runs missing usage, and projects repeatedly run without verification.

The default policy analyzes 30 days, marks a run at 100,000 tokens, requires two matching failures or two unverified project runs, retains at most five run IDs per finding, and emits at most 20 findings. The report includes these budgets and its truncation count. Invalid policy fails before analysis. Analysis never writes project files, configuration, manifests, or governance policy.

## Consequences

Local usage evidence no longer requires Langfuse or provider credentials. Remote delivery retains its existing broker authorization, sanitization, audit, payload, rate, and timeout controls. Runtime contracts cover Claude stored OAuth and API-key modes plus Codex ChatGPT and API-key modes by proving telemetry configuration is independent of personal authentication, while native event fixtures prove each provider normalization path.

The first meta-agent layer is deterministic and advisory. Recommendations are reproducible from the same history, time window, and policy, but the system does not yet track acceptance or prove that a recommendation improved later runs. Resource metrics, dashboards, approval-controlled changes, and automatic remediation remain separate work.
