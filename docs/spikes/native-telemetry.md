# Native telemetry spike

Step 0(b) of the v0 pivot: what the genuine Claude Code 2.1.302 and Codex 0.163.0 CLIs (the versions pinned in PR #120) emit over OTLP, which settings layers control it, and how a run's correlation context reaches every record. The spike informed the collection design in `pivot_proposal.md` section 3 and its implementation in `internal/runtelemetry` and `internal/evidence`.

## Result

- Both CLIs export logs, metrics, and spans over OTLP/HTTP JSON, and every content class (prompt, response, tool input, tool output) can be switched on. Codex's tool input cannot be switched off, which does not matter under the adopted design because collection never filters.
- Settings that only the launch environment carries are not authoritative. An agent-writable `~/.claude/settings.json` overrides Claude's environment, the repository's `.claude/settings.json` replaces Claude's resource attributes, and any process in the container can post OTLP with any attributes.
- Read-only managed settings (`/etc/claude-code/managed-settings.json`, `/etc/codex/managed_config.toml`) override user settings, repository settings, the process environment, and Codex `-c` flags for content switches, exporters, and endpoints.
- The run socket is therefore the authority for correlation. The host side stamps the `ai.*` context as request headers and the Collector writes it onto each resource, replacing whatever the container sent.

## Design consequence

Collection and use are separate layers. Collection stores everything each CLI emits, raw and unfiltered, plus the host-stamped `ai.*` context; each consumer filters on read according to its own risk. The pivot records this in section 3. The content-bearing attributes listed below are what a read-side filter for an AI-facing or off-host consumer has to handle; they are an observation for that consumer, not a filter applied on write.

## Method

`make telemetry-check` runs the black-box suite in `test/telemetry`. It builds each pinned CLI from the npm registry on the digest-pinned Node image used by PR #120, adds the product's `runtelemetry.ImageFiles` and `ImageEnv`, and runs it in rootless Podman with `--network none`. No credential is used: a scripted model on a Unix socket answers the first turn with one shell tool call and the second with a final answer, so each run produces a prompt, a response, tool input, and tool output, each marked by a random sentinel. Claude uses the Anthropic Messages stream with a placeholder `CLAUDE_CODE_OAUTH_TOKEN`; Codex uses a custom `responses` provider with `gpt-6-sol`, whose only shell path in 0.163.0 is the namespaced JavaScript `functions.exec` tool calling `tools.exec_command`.

The OTLP path is the production one: the image's loopback forwarder on port 4318, a Unix socket at the run-socket path, the `runtelemetry.Forwarder`, and the pinned Collector started by `runtelemetry.Collector` writing into an `evidence.Root`. Each CLI also gets a hostile launch environment, hostile user settings, hostile repository settings (Claude), and hostile `-c` flags (Codex) that turn telemetry off, divert the endpoint, and forge `ai.run.id` and `ai.agent`. Assertions read only the stored evidence through `runtelemetry.Summarize` and the raw `otel.jsonl`.

Throwaway mutation probes confirmed the scenarios bite: without the managed settings nothing is stored, without the Collector's resource processor records land under the forged run ID, and without the pinned Claude export intervals a container killed three seconds after its tool call stores nothing. `AI_CREW_CLAUDE_VERSIONS` and `AI_CREW_CODEX_VERSIONS` select other versions for upgrade checks.

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
- The Collector's OTLP/HTTP receiver accepts a request without the context headers and its file exporter silently drops records that lack the `group_by` attribute, so the config inserts `_unattributed` as the fallback run directory instead.
- The Collector file exporter creates files with mode 0644, so the evidence root itself must be owner-only.

## Collection implementation

| Concern | Owner | Budget and failure policy | Scenario in `test/telemetry` |
| --- | --- | --- | --- |
| Host-stamped `ai.*` context | `runtelemetry.Context`, `Forwarder` | Run IDs of 1 to 64 letters, digits, or hyphens; values printable ASCII within 512 bytes; only `Content-Type` and `Content-Encoding` pass through | Every content class is stored with host context despite agent overrides |
| Run-socket OTLP route | `runtelemetry.Forwarder` | `POST /otlp/<agent>/v1/<signal>` only; 32 MiB per request; 10 s Collector timeout; forwarded, rejected, and failed counts appended to `run.jsonl` | Oversized and unroutable requests are refused and counted; the agent keeps working when the Collector is down and the loss is recorded |
| Collector | `runtelemetry.Collector`, `CollectorConfig` | Pinned contrib 0.161.0; `--network none`; started or reused by configuration fingerprint within 30 s or refused; restart on failure; 1 s file flush | The collector is reused until its configuration changes; unstamped telemetry is kept under `_unattributed` |
| Loss visibility | `Forwarder.RecordStats`, `Summarize` | Per-run forwarded, rejected, and failed counts in `run.jsonl`; the Collector writes synchronously behind its receiver, so a refused request or a file that cannot be opened reaches the forwarder as an error status; a write that fails during the 1 s buffered flush, such as on a full disk, is not reported back, and the free-space floor guards against it | The agent keeps working when the Collector is down and the loss is recorded |
| Bounded loss on kill | `ClaudeManagedSettings`, `ImageEnv` | Logs and spans every 1 s, metrics every 10 s | Telemetry exported before a container kill is stored |
| Agent settings | `ImageFiles` | Every content switch on; root-owned and not writable by the agent user; Codex tool results capped at 1 MiB | Every content class is stored; the agent user cannot replace the managed files |
| Evidence root | `evidence.Root` | Owner-only 0700 root, no symlink; 1 GiB free-space floor; 5 GiB telemetry budget pruning the oldest inactive runs, each deletion recorded and fsynced before it happens; audit and run files never pruned | Disk-only scenarios for root safety, the floor, and pruning |

Measured on one scripted tool turn: Claude stored 164 KB (14 log records, 4 metrics, 6 spans) and Codex 600 KB (21 log records, 51 metrics, 505 spans). Killed three seconds after the tool call, Claude still stored 150 KB (11 log records, 4 spans) and Codex 381 KB (14 log records, 428 spans). The suite runs in about 30 seconds with cached images.

Wiring that lands with its owners: `ai up` opens the evidence root, enforces the floor and the budget, starts the Collector, and creates the run; the broker mounts the forwarder on the run socket and records its counters; the image builder writes `ImageFiles` and `ImageEnv`; `ai logs` renders `Summarize`. Hook adapters follow in their own change and reuse the same route. Phoenix is a second Collector destination on top of this; per the pivot, its span mapping applies on the way to Phoenix, never to stored evidence.

Notes for consumers, partly from the CodeAlive vibe-stack observability guide: verify a read-side filter against stored records, because exporters rename fields; keep a missing attribute absent rather than zero; and reconcile model-proxy request counts with `api_request` records to find calls that native telemetry does not report, such as the Codex `turn-costs` request.

## Not verified

Real provider authentication, where subscription logins may add identity attributes; interactive TUI sessions; OTLP protobuf and gRPC transports; MCP tools and subagents; and Collector behaviour under sustained load or a full disk.
