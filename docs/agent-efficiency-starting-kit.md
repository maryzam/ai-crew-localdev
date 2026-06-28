# Agent efficiency baseline

This baseline reduces avoidable tokens without adding a large agent framework.

## Default controls

| Control | Enforced behavior |
|---|---|
| Managed runs | ai-agent captures a local usage snapshot before and after Claude or Codex and stores the delta on the run. |
| Telemetry | The normalized run goes to local JSONL and the configured OTLP or Langfuse backend. A future meta-agent reads this data, not ccusage output. |
| Verification | Passing output is hidden. Failed output is limited to 60 lines and 256 KiB. Retries default to 2 and cannot exceed 10. |
| Command evidence | ai-agent check hides passing output, limits each log to 10 MiB, and keeps at most 20 logs or 20 MiB. |
| Guidance | Small global files are installed only when missing. Existing user files are not changed. |
| Search | The generic image includes rg. Project containers can use rg or git grep. |

The usage flow is automatic:

```text
Claude or Codex local logs
  -> ccusage read-only adapter
  -> ai-agent normalized run usage
  -> local run history and OTLP
  -> Langfuse dashboards
  -> future meta-agent analysis
```

ccusage is an adapter, not the source of truth or a separate workflow. ai-agent runs show displays its result. ai-agent usage remains an optional aggregate diagnostic command. The adapter runs offline with a small environment allowlist. Each snapshot has a one-second limit. If it is missing or slow, the run continues and usage is omitted.

The delta is an estimate. Concurrent runs from the same provider can overlap. Exact provider session correlation remains future work and must not be fabricated.

## Memory

- Keep stable team rules in AGENTS.md.
- Keep procedures in docs or skills and load them only when needed.
- Keep generated memory local.
- Enforce security and spend limits in code, tests, hooks, or policy.

## Proof

Use comparable tasks and the same quality gate. Compare at least five runs when practical. Track median input, output, cache, total tokens, retries, outcome, and duration. Reject a change if quality or security evidence falls.

## Tool choices

| Tool | Decision |
|---|---|
| ECC | Copy its profile and audit ideas. Do not install its large default skill and hook set. |
| no-mistakes | Keep disposable worktree and evidence ideas. Do not add another push and PR controller. |
| AXI | Copy bounded fields, explicit empty states, and truncation patterns. |
| caveman | Use concise output as a style cue. Do not treat its small output benchmark as end-to-end proof. |
| agent-browser | Keep optional. Browser egress, auth state, and prompt injection need a separate locked profile. |
| VectorCode | Benchmark only for large repos where targeted search fails. Do not add an index by default. |
| ccusage | Use as a pinned, read-only adapter behind ai-agent telemetry. |

Add another tool only after documenting its access, stored data, version pin, output limit, owner, rollback, and measured benefit.
