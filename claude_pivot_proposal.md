# Pivot Proposal: from governed workspace to least-privilege credential broker

Date: 2026-09-03. Scope: strategic review of ai-crew-localdev against what the official agent harnesses now ship out of the box, and the pivot that follows from it.

## Evidence basis

Claims about the harnesses were checked against the binaries installed on this machine, not from memory: `claude 2.1.251`, `codex-cli 0.149.0`, `gemini 0.38.2`. Method was help surfaces plus string and symbol extraction from the shipped binaries, and the live local configuration. Repo side: 23.5k non-test and 22k test Go LOC across 19 ADRs.

Confirmed: settings keys, flags, and mechanisms present in the shipped binaries. Not confirmed: runtime default for `sandbox.enabled` — extraction was inconclusive. Moot for the argument below, since no sandbox block is configured on this host either way.

## 1. What the official harnesses have commoditized

This design was set when none of the following existed. Most of it now ships in the box.

| Repo capability | Now native |
|---|---|
| Container isolation as the containment story | `claude` ships a bubblewrap + seccomp sandbox (`bwrap`, `seccomp`, `autoAllowBashIfSandboxed` present in the binary); `codex sandbox` is a top-level subcommand with `-s read-only\|workspace-write\|danger-full-access`; `gemini -s` |
| Network egress control | `claude` sandbox settings include `allowedDomains` / `deniedDomains`; `codex --sandbox-state-disable-network`, `network_access`, seccomp `ConnectTcp`/`BindTcp` filters |
| Policy and approval governance | `claude --permission-mode` (6 modes), `permissions` + `managedSettings` admin policy, `--restricted`, `--allowedTools`; gemini ships a full Policy Engine with `--policy` and `--admin-policy`; `codex -P/--permission-profile` |
| Private checkout then explicit apply | `claude -w/--worktree`, `gemini -w/--worktree`, and literally `codex apply` — "Apply the latest diff produced by Codex agent as a `git apply` to your local working tree" |
| Native usage capture | `claude` emits `claude_code.token.usage`, `claude_code.cost.usage`, `claude_code.llm_request`, `claude_code.tool.execution` over OTel via `CLAUDE_CODE_ENABLE_TELEMETRY` + `OTEL_EXPORTER_OTLP_*`; `codex` emits `otel.*` spans and `codex.api_request` |
| Broker-shaped auth/telemetry process | `claude gateway` — "Run the enterprise auth/telemetry gateway" — first-party, aimed at the same slot |
| Budgets | `claude --max-budget-usd`, print mode only |
| Doctor and health | `claude doctor`, `codex doctor`; `ai-agent doctor` is the third |
| Session lifecycle | resume / fork / background / attach / logs / stop / queue / archive across all three |
| Extensibility for interception | hooks, skills, plugins, MCP in all three; `gemini hooks`, `codex plugin` |
| Machine-readable output for a meta-agent | `--output-format json\|stream-json`, `--json-schema` (claude); `-o stream-json` (gemini) |

Net: containment, policy, isolate-and-apply, usage telemetry, and headless invocation are commoditized. That is the majority of the repo by LOC. The devcontainer/uphost/homestate/workspace group alone is ~4.5k non-test lines (~8k with tests) defending a position the vendors now hold.

### The credential-custody finding

Claude Code 2.1.251 also ships `sandbox.credentials`, with `envVars` and `files` masking: the real secret is replaced in the agent's environment by a sentinel, and a TLS-terminating proxy (`sandbox.network.tlsTerminate`, own CA) substitutes the real value on egress only to `injectHosts` destinations. It supports `extract` for structured values, `decode: "jwt"` with `maskClaims`, and `awsPairs`/`sigv4` re-signing. It is honored only from user, managed, or `--settings` sources, never project settings.

This is architecturally the same move as the broker, taken from the network side instead of the credential-helper side. It narrows the moat considerably and it is the single most important input to the pivot.

## 2. What is still genuinely differentiated

1. **Short-lived, downscoped minting.** Masking protects a secret in transit; it does not shrink what that secret can do. Claude injects a credential you already hold, at whatever scope you hold it. The broker mints a per-session GitHub App installation token, repo-scoped, permission-subset validated, escalation rejected (security invariants 2 and 3). Nothing in any harness does this.
2. **Audit-before-issue durability.** Credential issuance is withheld unless audit intent is durably recorded, and storage failure latches broker health (invariant 6). No harness ties issuance to evidence.
3. **Cross-vendor normalization.** `ai_agent.usage.*` with `source`, `confidence`, `precision`, `status`. Three vendors will never converge on a schema; reconciling them is structurally defensible.
4. **Live warn/stop budgets that halt a run.** `--max-budget-usd` is print-mode and single-vendor. Per-project, cross-agent, interactive, enforced from native usage events is unavailable anywhere.
5. **Findings ledger and `runs analyze`.** Durable cross-project, cross-agent retrospection with fingerprint accept/dismiss/reopen. No harness looks across sessions, let alone across tools.
6. **`ai-agent-manifest/v2`** as the single per-project declaration. Every CLI has its own config dialect; the layer above them is the right place to stand.
7. **The rigor infrastructure.** `quality/boundaries`, `sourcecomments`, `securityclaims` generated from a tested registry, `docsexamples`, telemetry schema drift check. Valuable regardless of what the product becomes.

## 3. Risk and strictness: OOTB versus this tooling

The two systems defend different axes.

| Axis | Claude / Codex OOTB | ai-crew-localdev |
|---|---|---|
| Kernel process containment | Yes — bubblewrap + seccomp (Claude); Landlock + seccomp (Codex) | No — explicit non-goal; devcontainer confinement is real but a same-UID host process spoofs the marker |
| Egress control | Yes — domain allow/deny, MITM proxy, network syscall filters | No — raw network calls are a stated non-goal |
| Static credential custody | Yes — masking plus egress injection | Yes — helper plus wrapper |
| Short-lived downscoped minting | No | Yes |
| Audit-before-issue durability | No | Yes |
| Cross-agent coverage | Per-vendor, non-portable | One boundary for both |
| Blast radius if the boundary fails | Full-scope PAT | One repo, roughly one hour |

**Where OOTB is stricter.** Kernel-enforced filesystem and syscall boundaries, and default-deny egress. The `gh` wrapper is explicitly the supported command path, not a sandbox boundary — an absolute-path `gh` or a raw socket walks past it; Landlock does not care what path a binary is at. The P1 runtime-identity gap, the supply-chain hardening item, and "real-tool removal / egress policy" are all already solved on the harness side.

**Where this repo is stricter.** If Claude's proxy injects a `gho_...` token, that request carries every scope the token has, across every repo it can reach. Invariant 3 denies a cross-repo request outright, because the token was never minted for it. And neither harness ties credential issuance to durable audit evidence.

**Weak on both sides.** Claude's permission system is model-mediated for anything the sandbox does not cover. Codex `trust_level = "trusted"` disables approval for a whole project.

### Operative strictness on this host

- `~/.claude/settings.json` has `permissions.allow: [Bash, Read, Write, Edit, Glob, Grep]` — blanket Bash approval — and no `sandbox` block at all. None of the containment above is switched on.
- `~/.codex/config.toml` marks 10+ project paths `trust_level = "trusted"`.
- `gh auth status` returns two logged-in accounts with `gist, read:org, repo, workflow`, keyring-backed. Any command either agent runs reaches both.

The strictest configuration in the comparison is the one not currently in the path, and the two that are in the path are running at their loosest settings. The gap between designed strictness and operative strictness is the actual risk story.

## 4. Proposed pivot

Ranked by value over effort.

**A. Demote the container from default path to option.** Broker credentials into a host session running under the harness's own sandbox (`claude` sandbox with `allowedDomains`, `codex -s workspace-write`). Keeps the minting guarantee, retires ~4.5k non-test lines, and removes the P1 runtime-identity gap as a blocker because vendor kernel sandboxes now exceed what a devcontainer marker provides. The `git` and `gh` wrappers work on host PATH identically. Also collapses startup cost, which is the reason a full real run has not happened yet.

**B. Compose with `sandbox.credentials` instead of replacing it.** Mask `GH_TOKEN` and `~/.config/gh/hosts.yml`, run the harness sandbox, and let the credential helper supply the scoped token inside it. Result is kernel containment plus downscoped minting, for very little new code. This is the integration point that did not exist when the current design was set.

**C. Be the OTLP collector, not the relay.** Both CLIs already export. Run a local endpoint, point `OTEL_EXPORTER_OTLP_ENDPOINT` at it, and every session lands in run history — including hand-run ones. Today observation requires a managed run, which is the adoption blocker on the analyzer. Ingest and schema already exist.

**D. Ship the broker as a standalone credential helper.** `ai-agent-credential-helper` plus `gh` wrapper, configured via `git config credential.helper`, usable from any shell or harness with no launcher. Same security property, a fraction of the surface, composes with harness hooks rather than competing with the session model.

**E. Manifest to native-config compiler.** `.ai-agent/manifest.json` emits `.claude/settings.json` permissions and hooks, `AGENTS.md`, a `.codex/config.toml` profile, and gemini policy files. Pure translation, cheap, and it rides features the vendors now maintain. This is the only version of the north star that does not require out-building three funded teams.

**F. Ledger as a Claude Code skill or plugin.** Plugins and skills are the distribution channel. `runs analyze` reading the local ledger, invoked at the point of work, costs nearly nothing.

### Drop or freeze

- The P1 kernel/idmapped-UID containment work. Expensive, now approximated by vendor sandboxes, unjustified by a single-user workstation threat model.
- Supply-chain containment, real-tool removal, and egress policy from the hardening roadmap. Vendor-maintained and better than a devcontainer marker. Delete rather than defer.
- The bundled Langfuse stack becomes optional once any OTLP collector works.

### Consequence for positioning

The headline claim shifts from "governed containerized workspace" to "least-privilege, audited provider credentials for whichever agent you are running, with cross-agent spend and quality evidence". That is more honest about what is differentiated, easier to explain, and survives the next several vendor releases in a way the container story does not.
