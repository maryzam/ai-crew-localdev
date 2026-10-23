# Claude setup-token custody spike

## Question

Can a task container run the unmodified Claude Code CLI on a Claude subscription while holding only a placeholder credential, with the real long-lived `claude setup-token` held on the host and injected per request by a model proxy behind the run socket?

## Answer

Yes. The genuine CLI, interactive and with `-p`, runs in rootless Podman with `--network none` and reaches the model only through [`internal/modelproxy`](../../internal/modelproxy) on a mounted unix socket. The container never sees the real token, a placeholder copied out of the container is rejected by the provider, and model access stops when the proxy is down. Sessions resume across a CLI upgrade on the same agent home.

After `ai login claude`, `AI_CREW_CLAUDE_VERSIONS="<old> <new>" make claude-proxy-compat` resolves the current published generation, reproduces these results against the genuine CLI, and is the gate for adopting a new Claude Code version.

## Traffic outside the base URL

Observed with a logging CONNECT proxy as `HTTPS_PROXY`:

- With default settings, each `-p` run makes about 8 direct connections to `api.anthropic.com` that are not inference. They carry at most the placeholder.
- With `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, `-p` makes no direct connections; interactive runs still make two connections to `downloads.claude.ai`.
- Without `DISABLE_AUTOUPDATER=1`, interactive runs contact `downloads.claude.ai` and `registry.npmjs.org`.
- The WebFetch tool and fast mode contact `api.anthropic.com` directly, as documented.
- First-run interactive onboarding checks connectivity to `platform.claude.com` directly and refuses to start when that check fails. With onboarding and project trust already recorded in `.claude.json`, the TUI starts with only the model socket.

## Requirements for v0

- `ai up` sets `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and `DISABLE_AUTOUPDATER=1` and pre-seeds onboarding and project trust, so restricted egress cannot break first-run startup.
- When the broker socket is missing, the CLI sees a refused connection and retries with backoff for more than 60 seconds before failing. The in-container forwarder answers that case with a non-retryable error, or `ai up` caps retries, so the failure meets a deterministic time budget.
- A setup-token has no refresh token, so there is no host-side refresh. The proxy records and latches a provider response that explicitly identifies an authentication error; an unclassified 401 reaches the CLI without disabling the host login. `ai login claude` must provide the actionable replacement path.

## Vendor terms

The [LLM gateway](https://code.claude.com/docs/en/llm-gateway) documentation describes routing a claude.ai subscription login through `ANTHROPIC_BASE_URL` and requires the gateway to forward the OAuth `anthropic-beta` value; the proxy ensures that value is present and leaves the CLI unmodified. The [legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) page states that developers "may not collect, store, or intermediate Claude.ai credentials or session tokens" or route requests through Free, Pro, or Max credentials on behalf of their users, while it does not prevent an end user from signing in to the unmodified binary with their own subscription. One operator holding their own token, issued by Anthropic's own flow, on their own host for their own sessions reads as the permitted case. That is a reading, not confirmation; offering this to other users would fall on the prohibited side, and wider reliance should be confirmed with Anthropic first.

## Open

- The two `downloads.claude.ai` connections with nonessential traffic disabled are unexplained.
- One interactive startup request returned 429 without visible effect on the session.
