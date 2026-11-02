# Telemetry and Wire Capture

**Scope: reading a run's evidence with the greenfield `ai` command.** The legacy `ai-agent logs` command is described in the [CLI Reference](cli-reference.md).

`ai up` does not exist yet, so no real run records this evidence until it lands. The storage, the commands, and the agent settings below are ready for it.

## What is stored

Every path is under `${XDG_DATA_HOME:-~/.local/share}/ai-crew/evidence`, and every file is owner-only.

| Path | Contents | Pruned |
|------|----------|--------|
| `runs/<run>/otel.jsonl` | Everything the native CLIs emit over OpenTelemetry, with host-stamped `ai.*` context | Yes |
| `runs/<run>/wire/` | Exact model requests and responses, when wire capture is on | Yes |
| `runs/<run>/run.jsonl` | Telemetry and capture counters | No |
| `pruning.jsonl` | One record for each deletion, written before the deletion | No |

## `ai logs`

```text
ai logs [run-id]
```

Shows the latest run or the one you name: stored records, receiver losses, capture counts and losses, and one usage row per agent, session, and model with tokens, cache reads and writes, cache hit share, and estimated cost. A value a CLI did not report shows as `-` rather than zero. Claude Code reports an estimated cost; Codex reports none.

## `ai capture`

```text
ai capture status
ai capture on
ai capture off
ai capture export [run-id]
```

Wire capture stores each model request and response exactly as the container sent and received them. Native telemetry alone cannot do this: Claude Code cuts its inline bodies at about 60 KB and loses the system prompt, the tools, the cache markers, and the latest messages, and Codex exports no bodies at all.

Capture is **on by default**. `ai capture off` stops it for runs started afterwards; a running run keeps the state it started with. While off, nothing is written to `wire/`, and the run records that capture was off.

`ai capture export` prints one JSON line per exchange. Text bodies appear under `text`, and any other bytes under `base64`. Each exchange also keeps its status, timings, the request and correlation headers, and any rate-limit headers the provider sent. Join an exchange to its native `api_request` record with the provider's `Request-Id` response header. Export stops at damaged chunks rather than returning them. It prints every readable exchange but exits with an error when any record was unreadable, and it says how many were omitted.

Captured traffic contains whatever passed between the agent and the model, including file contents, command output, and any secrets in them. It stays on your disk. Filter it yourself before sending it anywhere.

Authentication headers, cookies, and URL query strings are never stored. Capture wraps the model proxy from the outside, so it only ever sees the run placeholder and responses that have already had the host credential removed.

## Limits and failures

| Situation | Limit | What happens |
|-----------|-------|--------------|
| All telemetry and capture | 50 GiB | Before each run, `ai up` will delete and record the oldest finished runs' `otel.jsonl` and `wire/`, and refuse to start if active runs alone exceed the budget |
| Free space on the evidence volume | 1 GiB | Telemetry and capture stop and are counted as failed; the agent keeps working |
| One request or response body | 64 MiB | The traffic passes untouched; only the first 64 MiB is kept and the exchange is marked incomplete |
| Bodies held in memory | About 256 MiB across running requests | The traffic passes untouched; further bytes are not kept and those exchanges are marked incomplete |
| Evidence directory busy | 2 seconds per write | The exchange or telemetry batch is counted as failed; model traffic never waits for storage |
| Stream aborted midway | None | What reached the agent is kept and the exchange is marked incomplete |
