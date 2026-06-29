# Agent efficiency baseline

This baseline reduces avoidable tokens without adding another agent framework.

## Default controls

| Control | Enforced behavior |
|---|---|
| Managed runs | Claude and Codex send native OpenTelemetry to a loopback relay. Request token counts are stored on the run. |
| Credentials | The broker reads Langfuse keys from an owner-only file. Agents receive only a random relay token. |
| Privacy | Prompt, tool content, raw API bodies, unknown fields, and ambient exporter settings are blocked. |
| Verification | Passing output is hidden. Failed output is limited to 60 lines and 256 KiB. Retries default to 2 and cannot exceed 10. |
| Command evidence | `ai-agent check` hides passing output, limits each log to 10 MiB, and keeps at most 20 logs or 20 MiB. |
| Guidance | Small global files are installed only when missing. Existing user files are not changed. |

The automatic path is:

```text
Claude or Codex native OpenTelemetry
  -> authenticated loopback relay
  -> normalized local run history
  -> sanitized OTLP traces in Langfuse
  -> future meta-agent analysis
```

Usage is marked with source, scope, precision, and confidence. Current Claude and Codex values are provider-reported per request. Cost is recorded only when the provider emits it. Missing values stay empty.

## Memory

- Keep stable team rules in `AGENTS.md`.
- Keep procedures in docs or skills and load them only when needed.
- Keep generated memory local.
- Enforce security and spend limits in code, policy, hooks, or tests.

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

Add a tool only after documenting its access, stored data, version pin, output limit, owner, rollback, and measured benefit.
