# Observability

**Scope: what a managed run records, and what you can learn from it.** Local run history, the Langfuse stack and how its keys stay in the broker, the advisory analyzer, and the findings ledger. The gates that *act* on runs are in [Quality Gates](quality-gates.md).

## Local run history (always on)

Every `ai-agent run` gets a stable run ID, writes local JSONL history, passes `AI_AGENT_RUN_ID` to the agent process, and stamps the same run ID onto broker audit metadata for session and credential events. No backend required.

History is written to `~/.config/ai-agent/run-telemetry.jsonl`, rotated at 10 MiB with one `.1` backup, kept at mode `0600`, and serialized across concurrent runs. It records run start and finish, project, agent, model evidence, command result, verification result, retry count, duration, and provider-reported usage when available. Missing values are omitted.

Full prompts and verify commands are **not** recorded; verify commands are stored as hashes.

```bash
ai-agent runs list
ai-agent runs show <run-id>
```

Claude and Codex usage collection runs locally even when Langfuse is not configured.

## Langfuse

```bash
ai-agent up --langfuse --workspace ~/github   # alongside the dev environment
make langfuse-up                              # or manage the stack on its own
make langfuse-down
```

This starts Postgres, ClickHouse, Redis, MinIO, and Langfuse web + worker as Compose services. On first run it creates a `.env` from the bundled `.env.example` — review and change the secrets before production use. The file must stay owner-only (mode `0600`).

Like the devcontainer, the stack definition ships inside the binary: a release install materializes it under `~/.local/share/ai-agent/langfuse/`, and your `.env` lives beside it. The embedded copy is always the default — the ambient working directory is never trusted. To iterate on a checkout's `contrib/langfuse/` instead, set `AI_AGENT_DEV_ASSETS_DIR` to the checkout root explicitly.

The UI is available only on **http://127.0.0.1:3000**. The loopback binding keeps the local bootstrap account off the LAN. Starting the stack alone gives you the UI; run `ai-agent up --langfuse` once to configure brokered ingestion.

### How ingestion stays sealed

`ai-agent up --langfuse` reads the project ID and OTLP endpoint from the stack's `.env`, adds a `langfuse:project:<id>` resource to broker policy, and reloads the broker. The launcher gives the agent only a random token for an authenticated loopback relay. Sanitized traces are published through the bound broker session; the broker-side Langfuse provider reads the owner-only key file, validates the OTLP projection again, and performs external egress **without returning the keys or endpoint to the launcher**.

Claude and Codex send native OTLP logs and traces to that relay. Logs provide normalized request usage for local history; Langfuse configuration only enables the optional sanitized trace export path.

Traces are rebuilt from an allowlist before export, and rejected by the broker if they exceed 1 MiB or contain fields outside the approved projection. Broker egress has a three-second request deadline and independent limits of 120 deliveries per session and 240 per resource per minute. Each accepted attempt records payload size and SHA-256 in broker audit evidence, before and after egress. Prompt content, tool content, raw API bodies, unknown fields, and ambient exporter settings are blocked. If export fails, local run history remains available.

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
