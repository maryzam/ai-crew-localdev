# Raw body capture

## Question

Can native telemetry supply the complete model requests and responses that a deep analysis of agent behaviour needs: why a cache missed, which instructions or tools fill the context, and what the model saw before a poor decision?

## Answer

No. Inline bodies are unusable for that analysis, so complete bodies are captured around the model proxy. The per-request numbers in the native `api_request` event are complete and remain the source for usage and cost.

## Measurements

The pinned Claude Code 2.1.302 ran against a scripted local model with a 200,000 character prompt and no real credential.

| Mode | Request recorded | Holds |
|------|------------------|-------|
| On the wire | 317,542 characters | `model`, `messages`, `system`, `tools`, in that order |
| `OTEL_LOG_RAW_API_BODIES=1` | 61,440 characters | `model` and the oldest part of `messages`; no `system`, `tools`, cache markers, or latest messages |
| `OTEL_LOG_RAW_API_BODIES=file:<dir>` | 317,542 characters | Everything, in a directory the agent can write |

`system` and `tools` were about 106 KB in this run, so every inline body is cut, and in a long session each record repeats the same opening messages. The CLI's code also redacts thinking and withholds memory operations in both modes.

Native transcripts hold `api-request-shape` records with `system` and `tools`, and `api-request` records with cache markers and parameters. The format is absent from the monitoring documentation. Across 6,521 local requests, 12,459 of 22,244 message references had no stored blob, so exact requests could not be rebuilt.

Codex exports no raw bodies over OpenTelemetry.

Estimated from local usage counts over three weeks at 4 bytes per token, whole Claude request bodies come to about 7 GiB across 6,264 requests, against about 0.4 GiB when each body and tool output is cut at 60 KB. Native Codex volume is about 300 KB to 600 KB per model response in the scripted run, or an estimated 2 to 4 GiB a month.

## Mechanism

[`internal/product/wirecapture`](../../internal/product/wirecapture) is an HTTP handler wrapper placed outside the model proxy. The proxy, the credential, and the response redactor stay inside it, so it sees only the placeholder and redacted responses, and it stores an allowlist of headers rather than filtering a denylist.

Bodies are split by content into chunks averaging about 6 KiB, at most 32 KiB, and each distinct chunk is stored once per run. Across 40 seeds of a simulated 120-turn conversation, this stored a median of 4.0% of the wire bytes and at worst 9.4%; a run with little repeated history stores far more. This needs no knowledge of either provider's format, reproduces each body byte for byte, and tolerates insertions in the middle of a request, which is where each new message lands. Storing the exact bytes rules out splitting on parsed JSON, which would change key order and whitespace.

Writes happen after the response on a background goroutine, under a memory budget, so slow or failing storage cannot delay model traffic. Any failure is counted and recorded.

## Not verified

- Live subscription traffic. The scenarios use the genuine CLIs against scripted models, so the rate-limit header names, the `Request-Id` join, request compression on the real backends, and real deduplication ratios are unconfirmed until `ai up` runs.
- `cleanupPeriodDays` in managed settings. It is set, but a short print-mode run did not delete old transcripts even without it, so no automated check proves it takes effect.
