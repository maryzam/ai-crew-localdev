# Native telemetry spike

Step 0(b) of the v0 pivot: what the genuine Claude Code 2.1.302 and Codex 0.163.0 CLIs (the versions pinned in PR #120) emit over OTLP, which settings layers control it, and how a run's correlation context reaches every record. The spike informed the collection design in `pivot_proposal.md` section 3 and its implementation in `internal/runtelemetry` and `internal/evidence`.

## Result

- Both CLIs export logs, metrics, and spans over OTLP/HTTP JSON, and every content class (prompt, response, tool input, tool output) can be switched on. Codex's tool input cannot be switched off, which does not matter under the adopted design because collection never filters.
- Settings that only the launch environment carries are not authoritative. An agent-writable `~/.claude/settings.json` overrides Claude's environment, the repository's `.claude/settings.json` replaces Claude's resource attributes, and any process in the container can post OTLP with any attributes.
- Read-only managed settings (`/etc/claude-code/managed-settings.json`, `/etc/codex/managed_config.toml`) override user settings, repository settings, the process environment, and Codex `-c` flags for content switches, exporters, and endpoints.
- The run socket is therefore the authority for correlation and the owner of storage. It writes the `ai.*` context onto each resource, replacing whatever the container sent, and appends the record before acknowledging it.
- The pinned Collector file exporter cannot be the evidence store. With `append: false`, its default, it truncates a run file it reopens, so a Collector restart erased earlier records. It acknowledges a request once the record is in a `bufio.Writer` and discards flush errors. Unbuffered writes require `rotation`, which is unsupported with `append: true`. It also keeps up to 100 files open, so pruning an open file left later acknowledged records in a deleted inode. The Collector therefore only fans out to views.
- The agent must not own the container-side forwarder. Started under the agent user, it can be killed and later exports never reach the host. The entrypoint starts it as container root and drops to the agent user with `setpriv`.

## Design consequence

Collection and use are separate layers. Collection stores everything each CLI emits, raw and unfiltered, plus the host-stamped `ai.*` context; each consumer filters on read according to its own risk. The pivot records this in section 3. The content-bearing attributes listed below are what a read-side filter for an AI-facing or off-host consumer has to handle; they are an observation for that consumer, not a filter applied on write.

## Method

`make telemetry-check` runs the black-box suite in `test/telemetry`. It builds each pinned CLI from the npm registry on the digest-pinned Node image used by PR #120, adds the product's `runtelemetry.ImageFiles` and `ImageEnv`, and runs it in rootless Podman with `--network none`. No credential is used: a scripted model on a Unix socket answers the first turn with one shell tool call and the second with a final answer, so each run produces a prompt, a response, tool input, and tool output, each marked by a random sentinel. Claude uses the Anthropic Messages stream with a placeholder `CLAUDE_CODE_OAUTH_TOKEN`; Codex uses a custom `responses` provider with `gpt-6-sol`, whose only shell path in 0.163.0 is the namespaced JavaScript `functions.exec` tool calling `tools.exec_command`.

The OTLP path is the production one. The container is launched as the product must launch it: `--userns keep-id:uid=1000,gid=1000 --user 0:0`, so the operator's UID maps to the agent user whatever the host UID is. The root entrypoint starts the loopback forwarder on port 4318 to the run-socket path and drops to the agent user. The `runtelemetry.Receiver` on that socket writes into an `evidence.Root`. Each CLI also gets a hostile launch environment, hostile user settings, hostile repository settings (Claude), and hostile `-c` flags (Codex) that turn telemetry off, divert the endpoint, and forge `ai.run.id` and `ai.agent`, and the agent kills every process it can signal before the CLI starts. Assertions read only the stored evidence through `runtelemetry.Summarize` and the raw `otel.jsonl`. Host-only scenarios cover receiver restarts, concurrent pruning and acknowledged writes, storage failures, busy locks, and refused requests without Podman.

Throwaway mutation probes confirmed the scenarios bite:
- Without the managed settings, nothing is stored.
- Without replacing container-supplied context keys, the forged run ID survives in stored records.
- Without the pinned Claude export intervals, a container killed three seconds after its tool call stores nothing.
- Opening with `O_TRUNC`, the restart scenario keeps one of two records.
- Without the privilege drop, the agent kills the forwarder and nothing is stored.

`AI_CREW_CLAUDE_VERSIONS` and `AI_CREW_CODEX_VERSIONS` select other versions for upgrade checks.

## Claude Code 2.1.302

| Signal | Observed |
| --- | --- |
| Logs | `user_prompt`, `assistant_response`, `api_request`, `api_request_body`, `api_response_body`, `tool_decision`, `tool_result`, `plugin_loaded`, `managed_settings_resolved` |
| Metrics | `claude_code.session.count`, `claude_code.token.usage`, `claude_code.cost.usage`, `claude_code.active_time.total` |
| Spans | `claude_code.interaction`, `claude_code.llm_request`, `claude_code.tool`, `claude_code.tool.execution`, `claude_code.tool.blocked_on_user`, with `gen_ai.*` attributes; emitted only with `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1` |

Content switches: `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS`, `OTEL_LOG_TOOL_CONTENT`, and `OTEL_LOG_RAW_API_BODIES`. With them off, content attributes are present as `<REDACTED>` and only `bash_argv0` and `bash_command_class` describe commands; no sentinel was exported.

Content-bearing attributes with the switches on: `user_prompt.prompt` and `prompt_text`, `assistant_response.response`, `api_request_body.body`, `api_response_body.body`, `tool_decision.tool_parameters`, `tool_result.tool_input` and `tool_parameters`, `claude_code.interaction.user_prompt`, and `claude_code.tool.full_command` with its `tool.output` span event. `api_request_body` was about 118 KB per request in this minimal run because every request resends the system prompt and history. One tool turn stored 164 KB, against 28 KB with content off.

Settings precedence observed: `env` in `~/.claude/settings.json` overrides the launch environment, including content switches, exporters, and `OTEL_RESOURCE_ATTRIBUTES`. `env` in the repository's `.claude/settings.json` cannot turn content on or divert the endpoint, but it does replace `OTEL_RESOURCE_ATTRIBUTES`. Managed settings override both and the process environment.

## Codex 0.163.0

| Signal | Observed |
| --- | --- |
| Logs | `codex.conversation_starts`, `codex.user_prompt`, `codex.api_request`, `codex.sse_event`, `codex.startup_phase`, `codex.turn_ttft`, `codex.tool_decision`, `codex.tool_result`, and an unnamed `flushing OTEL metrics` record |
| Metrics | 51 instruments, including `codex.turn.token_usage`, `codex.turn.e2e_duration_ms`, `codex.tool.call`, `codex.api_request`, and startup, plugin, skills, and storage internals |
| Spans | 136 span names from Rust `tracing` instrumentation (`run_turn`, `session_task.turn`, `handle_responses`, `exec`, `exec_command`, ...) with `code.file.path` and thread attributes; only `handle_responses` carries `gen_ai.usage.*`, and there are no OpenInference or `gen_ai` operation spans |

Content switches: `[otel] log_user_prompt`, `log_agent_responses`, `log_guardian_assessments`, and `[otel.tool_result] max_bytes`. With `max_bytes = 0` the output becomes `[... telemetry preview truncated ...]`. `codex.tool_result.arguments` always holds the complete tool input, for both the outer `exec` call and the nested `exec_command`. `codex exec` exported no response text even with `log_agent_responses = true`.

Content-bearing attributes with the switches on: `codex.user_prompt.prompt`, `codex.tool_result.arguments`, and `codex.tool_result.output`. One tool turn stored 600 KB, dominated by spans.

Settings precedence observed: `/etc/codex/managed_config.toml` overrides `CODEX_HOME/config.toml` and `-c` flags for content switches, `tool_result.max_bytes`, and exporter endpoints. Resource attributes come only from `OTEL_RESOURCE_ATTRIBUTES` in the process environment.

## Shared observations

- With default intervals both CLIs exported one batch per signal at process exit in short runs, so a killed Claude container lost everything. The pinned intervals bound the loss to the last export interval.
- Static OTLP headers (`OTEL_EXPORTER_OTLP_HEADERS`, Codex `headers = { ... }`) arrived on every export, but like resource attributes they are claims from a process the agent controls.
- Codex posted `POST /v1/analytics/codex/turn-costs` (body: `turn_ids`) to the model provider base URL. Whether the PR #120 proxy allows that path is a model-proxy decision outside this change.
- Local runs passed while CI failed with `Permission denied` on the sockets and `EACCES` on agent settings because the host UID happened to equal the image's agent UID. The explicit user namespace mapping removes that dependence.

## Collection implementation

| Concern | Owner | Budget and failure policy | Scenario in `test/telemetry` |
| --- | --- | --- | --- |
| Host-stamped `ai.*` context | `runtelemetry.Context`, `Receiver` | Run IDs of 1 to 64 letters, digits, or hyphens; values printable ASCII within 512 bytes; every context key the container supplies is replaced; all other fields, including 64-bit integers, are kept | Every content class is stored with host context despite agent overrides; the receiver replaces container claims and keeps the rest |
| Run-socket OTLP route | `runtelemetry.Receiver` | `POST /otlp/<agent>/v1/<signal>` with OTLP/HTTP JSON, identity or gzip, at most 32 MiB before and after decompression; anything else is refused and counted | The receiver refuses requests outside the telemetry contract |
| Storage | `evidence.Root.AppendTelemetry` | Each request takes the stable run-directory lock, appends, and closes before the response; lock acquisition has a 2 s budget and returns `ErrEvidenceBusy` on timeout; an error returns 500 and is counted; a restart or reopen never truncates | Telemetry stored before a receiver restart is kept; storage and busy-lock failures are returned, counted, and recorded on recovery; the agent keeps working when storage fails |
| Pruning | `evidence.Root.PruneTelemetry` | Pruners serialize on the runs-directory lock, then take each run-directory lock before recording a deletion durably and removing the file; both locks have a 2 s budget and return `ErrEvidenceBusy` on timeout; active-only excess returns `ErrTelemetryBudgetExceeded` | Concurrent pruning never hides acknowledged telemetry; the budget prunes oldest inactive runs and reports active excess |
| Loss visibility | `Receiver.RecordStats`, `Summarize` | Stored, rejected, and failed snapshots in `run.jsonl` carry a receiver instance ID, are retried while storage is failing, and are aggregated across receiver restarts | Storage failures are recorded on recovery; counters survive receiver restart |
| Container forwarder | `ImageFiles` entrypoint | Starts as container root, refuses to run otherwise, and drops to the agent user | Every content class is stored even after the agent kills every process it can signal |
| Bounded loss on kill | `ClaudeManagedSettings`, `ImageEnv` | Logs and spans every 1 s, metrics every 10 s | Telemetry exported before a container kill is stored |
| Agent settings | `ImageFiles` | Every content switch on; root-owned and not writable by the agent user; Codex tool results capped at 1 MiB | The agent user cannot replace the managed files |
| Evidence root | `evidence.Root` | Owner-only 0700 root, no symlink; 1 GiB free-space floor; 5 GiB telemetry budget; audit and run files never pruned | Root safety and the floor |

Measured on one scripted tool turn: Claude stored 164 KB (14 log records, 4 metrics, 6 spans) and Codex 600 KB (21 log records, 51 metrics, 505 spans). Killed three seconds after the tool call, Claude still stored 150 KB (11 log records, 4 spans) and Codex 439 KB (14 log records, 428 spans). The suite runs in about 25 seconds with cached images.

Wiring that lands with its owners: `ai up` opens the evidence root, enforces the floor and the budget, refuses a run when active runs exceed it, launches containers with the agent user mapping, and creates the run; the broker mounts the receiver on the run socket and runs `RecordStats`; the image builder writes `ImageFiles` and `ImageEnv`; `ai logs` renders `Summarize`. Hook adapters follow in their own change and reuse the same route. Phoenix arrives with a Collector that receives stored telemetry from the run socket and fans it out; per the pivot, its span mapping applies on the way to Phoenix, never to stored evidence.

Notes for consumers, partly from the CodeAlive vibe-stack observability guide: verify a read-side filter against stored records, because exporters rename fields; keep a missing attribute absent rather than zero; and reconcile model-proxy request counts with `api_request` records to find calls that native telemetry does not report, such as the Codex `turn-costs` request.

## Not verified

Real provider authentication, where subscription logins may add identity attributes; interactive TUI sessions; OTLP protobuf and gRPC transports; MCP tools and subagents; receiver throughput under sustained load; and a real full disk rather than an unwritable directory.
