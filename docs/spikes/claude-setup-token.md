# Claude setup-token custody spike

## Question

Can a task container run the unmodified Claude Code CLI on a Claude subscription while holding only a placeholder credential, with the real long-lived `claude setup-token` held on the host and injected per request by a model proxy behind the run socket?

## Answer

Yes, for Claude Code 2.1.296 and 2.1.302. The genuine CLI runs interactively and with `-p` in rootless Podman with `--network none`, reaches the model only through a host-side proxy on a mounted unix socket, and never sees the real token. A placeholder copied out of the container is rejected by the provider, and model access stops when the proxy is down.

## Setup

- Token: `claude setup-token` run on the host with a throwaway `CLAUDE_CONFIG_DIR`, saved to an owner-only file outside the repository (`~/.config/ai-crew-spike/claude-oauth-token`, mode 600). The earlier 401s came from a terminal-wrapped token truncated to 79 of 108 characters; the proxy now joins whitespace and validates the `sk-ant-oat01-` shape before use. A temporary PTY transcript from the earlier attempt contains a token and must be handled as a secret; its parent directory is owner-only and outside the repository.
- Proxy: [`internal/modelproxy`](../../internal/modelproxy), served on a unix socket. It forwards only model routes (`POST /v1/messages`, `POST /v1/messages/count_tokens`, `GET /v1/models`) and only when every presented credential equals the run placeholder. It replaces `Authorization` with the real bearer token, deletes `x-api-key`, forwards everything else unchanged to `https://api.anthropic.com`, and streams without buffering. It records a metadata-only event before forwarding and after completion, refuses with `x-should-retry: false`, and latches closed when evidence cannot be recorded.
- Container: [`testdata/claude/Containerfile`](../../internal/modelproxy/testdata/claude/Containerfile), a pinned Node base with `@anthropic-ai/claude-code@<version>` and `socat`, run with `--network none` and `--userns keep-id`. The run socket directory is mounted at `/run/model` and exposed by `socat` as `http://127.0.0.1:8080`, with `ANTHROPIC_BASE_URL=http://127.0.0.1:8080`, `CLAUDE_CODE_OAUTH_TOKEN=<placeholder>`, and `CLAUDE_CONFIG_DIR` on a per-test agent-home directory.
- Compatibility check: `AI_CREW_CLAUDE_TOKEN_FILE=<token file> AI_CREW_CLAUDE_VERSIONS="<old> <new>" make claude-proxy-compat` runs the checks below against the genuine CLI and is the gate for adopting a new Claude Code version.

## Results

| Check | Result |
|---|---|
| Full token with the native CLI on the host | `pong` |
| `-p` through the proxy, `--network none`, 2.1.296 and 2.1.302 | `pong` on both |
| Tool loop (Bash) through the proxy, both versions | succeeded in 2 turns |
| `--continue` from the agent-home volume, both versions | prior session resumed |
| Upgrade 2.1.296 → 2.1.302 on the same agent home, then `--continue` | prior session resumed |
| Interactive TUI with onboarding and folder trust pre-seeded in `.claude.json` | prompt answered; no login prompt shown |
| Placeholder sent directly to `api.anthropic.com` | HTTP 401 |
| A different placeholder on the run socket | HTTP 403 from the proxy, recorded as refused |
| Proxy stopped | agent retried with backoff and produced no answer before the 90 s timeout |
| Real token, or its first or last 40 characters, in any agent-home, output, or evidence file (about 1,200 files) | not found |
| Real token in any proxied response body | not found |
| Placeholder persisted in the agent home | not found; it lives only in the container environment |

The CLI sends the subscription credential as `Authorization: Bearer` with `anthropic-beta: oauth-2025-04-20`. Inference traffic is `POST /v1/messages?beta=true`, plus a credential-less `HEAD /api/hello` warm-up probe that the proxy refuses as a non-model route without effect on the session. Forwarded request headers are `anthropic-*`, `x-stainless-*`, `x-app`, `x-claude-code-session-id`, `user-agent`, and standard HTTP headers. One interactive startup request returned 429 without visible effect on the session.

## Traffic outside the base URL

With a logging CONNECT proxy as `HTTPS_PROXY`:

- Default settings, `-p`: about 8 direct connections to `api.anthropic.com` per run. They carry at most the placeholder, so they are not a leak, but they are not inference.
- `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`: no direct connections for `-p`; interactive runs still made two connections to `downloads.claude.ai`.
- The WebFetch tool works and contacts `api.anthropic.com` directly for its domain check, as documented.
- First-run interactive onboarding checks connectivity to `platform.claude.com` directly and refuses to start when that check fails. With onboarding already recorded in `.claude.json`, the TUI starts with only the model socket.
- Without `DISABLE_AUTOUPDATER=1`, interactive runs contact `downloads.claude.ai` and `registry.npmjs.org`.

## Implications for v0

- The pattern works: a model proxy behind the run socket with a loopback forwarder and placeholder credentials is enough for both `-p` and the interactive TUI.
- `ai up` should set `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and `DISABLE_AUTOUPDATER=1`, and should pre-seed onboarding and project trust so v0.5 restricted egress does not break first-run startup. Fast mode and WebFetch need direct `api.anthropic.com` egress, or the documented fast-mode proxy variables.
- Broker-down fails closed, but the CLI's retry backoff makes failure slow. The proxy's own refusals carry `x-should-retry: false`, but a missing broker socket reaches the CLI as a refused connection, so the in-container forwarder should answer it with a non-retryable error, or `ai up` should cap retries, to meet a deterministic time budget.
- A setup-token is a long-lived token with no refresh token, so there is no host-side refresh to perform. Expiry or revocation shows up as upstream 401, which the proxy should surface as an actionable `ai login claude` message and record as audit evidence.
- The compatibility check for a CLI upgrade is `make claude-proxy-compat`: a `-p` reply, a tool loop, `--continue`, the TUI, session continuity across the upgrade, placeholder rejection by the provider, broker-down failure, and a scan for the real token.

## Vendor terms

The [legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) and [LLM gateway](https://code.claude.com/docs/en/llm-gateway) pages were consulted during the spike. The gateway documentation describes routing a claude.ai subscription login through `ANTHROPIC_BASE_URL` and requires the gateway to forward the OAuth `anthropic-beta` value; this proxy does so and leaves the CLI unmodified. The legal page also states that developers "may not collect, store, or intermediate Claude.ai credentials or session tokens" and must not route requests through Free, Pro, or Max credentials on behalf of their users, while it does not prevent an end user from signing in to the unmodified binary with their own subscription. Here one operator holds their own token, issued by Anthropic's own flow, on their own host for their own sessions, which reads as the permitted case. It is still a reading, not confirmation; a product offered to other users would fall on the prohibited side, and the safe course before relying on it widely is to confirm with Anthropic.

## Open

- Codex has not been tested.
- The two `downloads.claude.ai` connections with nonessential traffic disabled are unexplained.
- The 429 on an interactive startup request is unexplained.
