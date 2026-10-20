# Observability

**Scope: what a managed run records, how the local Phoenix view works, and what you can learn from the evidence.** The gates that *act* on runs are in [Quality Gates](quality-gates.md).

## Authoritative run evidence

Native Claude and Codex OpenTelemetry reaches a loopback relay inside the managed run. The relay forwards raw logs, metrics, and spans through the authenticated broker session. The host receiver replaces container-supplied `ai.*` correlation fields with session context and appends the raw OTLP/HTTP JSON to `~/.config/ai-agent/evidence/runs/<run-id>/otel.jsonl` before acknowledging it. Phoenix mapping happens only after this append; it never changes stored evidence. The older Langfuse provider remains isolated for compatibility but is not part of this collection or view lifecycle.

Use `ai-agent logs` for the newest run or name a run explicitly. This path reads the owner-only evidence directory directly and works when Phoenix, its Collector, or the network is unavailable. It reports record and byte counts, receiver storage/refusal failures, Phoenix projection failures, and pruning evidence.

```bash
ai-agent logs
ai-agent logs <run-id>
```

`AI_AGENT_EVIDENCE_DIR` overrides the evidence root. Raw evidence can contain prompts, responses, tool input, tool output, and raw API bodies because collection enables every native content switch. Protect this directory as sensitive local data.

The older `~/.config/ai-agent/run-telemetry.jsonl` summary remains available to `ai-agent runs` for the advisory analyzer:

```bash
ai-agent runs list
ai-agent runs show <run-id>
```

## Phoenix

`ai-agent start` and `ai-agent up` start Phoenix by default with the selected Podman or Docker runtime. Image pulls have a five-minute budget and happen before the 90-second service startup budget. `--observability=false` skips the view without disabling evidence collection. A missing Compose provider, pull failure, timeout, or unhealthy service prints a warning and the run continues.

```bash
ai-agent start
ai-agent start --observability=false
make phoenix-up
make phoenix-down
ai-agent phoenix rebuild
```

The two-service stack contains Phoenix with its local SQLite store and an OpenTelemetry Collector. Both images have fixed versions and immutable multi-platform digests. The UI is available at **http://127.0.0.1:6006** and the host-side Collector receiver at `127.0.0.1:4319`; neither port binds a LAN interface, and the managed container reaches neither port through the runtime bridge. The Collector maps host-stamped project context and the default agent span kind on the path to Phoenix. It has a 512-item sending queue, one-second export timeout, and 30-second retry window.

After the raw append, the receiver places trace envelopes on a projection queue capped at 64 items and 64 MiB, then acknowledges storage without waiting for Phoenix. Each projection request has a 200 ms budget. A full queue, timeout, refusal, stopped Collector, or receiver shutdown increments `projection_failed`; none changes the stored record or agent outcome. Phoenix and its SQLite volume are a disposable view: restarting the stack does not touch raw evidence, and `ai-agent phoenix rebuild` replays retained spans in batches of at most 4 MiB. Use `ai-agent phoenix rebuild --run <run-id>` to replay one run. Each batch retains the 200 ms request budget, the command is interruptible, and Phoenix deduplicates repeated ingestion by trace and span ID. Collector logs describe downstream retry exhaustion.

Loopback is a host boundary, not per-process authentication. Any process running as another user on a shared host may be able to connect to these ports even though LAN peers and managed containers cannot. Use `--observability=false` on a shared host unless local-user access is otherwise restricted; raw evidence remains owner-only and `ai-agent logs` continues to work.

Like the devcontainer, the stack definition ships inside the binary and materializes under `~/.local/share/ai-agent/phoenix/`. The embedded copy is always the default; the ambient working directory is not trusted. Set `AI_AGENT_DEV_ASSETS_DIR` to an explicit checkout root only when iterating on `contrib/phoenix/`.

## Advisory analyzer

`ai-agent runs analyze` reads retained local history across projects and reports usage and cost coverage, repeated failures, retry waste, project-level high-token patterns, successful runs with missing usage, lower-quality usage that is not safe to base optimization decisions on, and projects that are mostly run without verification.

```bash
ai-agent runs analyze
ai-agent runs analyze --since 168h --high-tokens 75000 --min-unverified-percent 90 --json
```

Default policy: 30 days analyzed, runs marked at 100,000 tokens, two matching failures required, and a project flagged when it has at least two runs and 80 percent of them lack verification. At most five run IDs per finding and 20 findings total. Weak verification is prioritized ahead of aggregated high-token advice, so token volume cannot hide a quality gap. The report prints these budgets and the number of omitted findings.

The report is advisory: it never writes project files, manifests, configuration, or policy. Cost totals include only provider-reported values — unsupported or missing cost stays empty rather than being estimated. Telemetry configuration contracts cover Claude stored OAuth and API-key use plus Codex ChatGPT sign-in and API-key use, without recording which personal authentication mode was selected.

The coverage table classifies usage as trusted only when it is run-scoped, request-precision, and provider-reported. Other usage is reported separately with its source, scope, precision, and confidence; absent token totals count as missing. Provider identifiers are trimmed and normalized case-insensitively before grouping.

## Findings ledger

`ai-agent runs analyze` tracks every recommendation in a durable ledger. Each finding gets a stable fingerprint and a status, shown inline in the report. Accepted findings gain an evidence snapshot at acceptance time, and later reports show the measured outcome since acceptance (or that the finding is no longer flagged).

```bash
ai-agent runs analyze                  # recommendations, each with a fingerprint and status
ai-agent runs findings                 # tracked findings with statuses and dates
ai-agent runs findings accept <fp>     # accept and snapshot current evidence as the outcome baseline
ai-agent runs findings dismiss <fp>    # keep tracked without recommending action
ai-agent runs findings reopen <fp>     # return to open
```

Fingerprints cover the analyzer's full finding scope, so recommendations that share a kind and repository — different failure signatures, or per-agent usage gaps — stay separate findings. Fingerprint prefixes work anywhere a fingerprint is expected, as long as they are unique.

`accept` takes the same `--since` and threshold flags as `analyze`. Pass the flags you analyzed with, so acceptance snapshots the evidence you actually reviewed; the finding must be present in that window for the delta to have a real baseline.

The ledger lives in the config directory, is written atomically with owner-only permissions under a lock that serializes concurrent commands, and keeps at most 500 entries. Accepted findings are retained ahead of others, but the cap is hard so the ledger always stays loadable, and any pruning is counted and reported. A corrupt ledger fails the command instead of being overwritten.
