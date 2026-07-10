# ADR 0001: Credential-generic broker API

**Status:** Accepted — migration complete; legacy `mint_token` / v1 policy schema removed in the `refactor/credential-generic-broker` branch.

**Date:** 2026-05-23

## Context

The broker today mints GitHub App installation tokens only. `MintToken` request and `TokenResponse` types are GitHub-shaped; `github.go` lives directly in `internal/broker/`; policy talks about `allowed_repos`. The architectural intent is that the broker is the single trust boundary for *all* sensitive credentials an agent needs (AWS, AI provider keys, registry creds, etc.) — not just GitHub.

If the API stays GitHub-shaped, every new credential type either (a) grafts on awkwardly with a parallel method, or (b) never ships, and credentials end up taking ad-hoc paths back into the container.

## Decision

Make the broker API credential-type-generic *now*, before the second provider lands. Clean break — no backwards compatibility with the existing GitHub-only wire shape.

### Wire protocol

Rename `mint_token` → `mint_credential`. Request and response carry an explicit `credential_type` discriminator and a `resource` URI. Provider-specific fields live in opaque `params` / `credential` `json.RawMessage` blobs.

```
Request:  { "method": "mint_credential",
            "body":   { "session_id":      "...",
                        "bind_secret":     "...",
                        "credential_type": "github_app_installation",
                        "resource":        "github:repo:org/name",
                        "params":          { "permissions": { ... } } } }

Response: { "ok": true,
            "body": { "credential_type": "github_app_installation",
                      "resource":        "github:repo:org/name",
                      "credential":      { "token": "ghs_..." },
                      "expires_at":      "..." } }
```

Sessions take a list of `resources` rather than a single `repo`. Phase 1 launchers pass exactly one `github:repo:...` resource per session; multi-resource sessions are supported by the wire but not yet by the launcher UX.

### Resource URI format

`<provider>:<kind>:<identifier>` — colon-delimited, three components.

| URI                              | Provider | Kind | Identifier  |
|----------------------------------|----------|------|-------------|
| `github:repo:example-org/example-repo` | github | repo | `org/name`  |
| `aws:role:arn:aws:iam::123:role/x` | aws    | role | role ARN    |

Identifiers may themselves contain colons (AWS ARNs). Parsers split on the first two colons only.

### Credential types (registered constants)

Go constants in `broker/api.go` next to the existing `Method*` constants:

```go
const (
    CredentialTypeGitHubAppInstallation = "github_app_installation"
    // future: CredentialTypeAWSAssumeRole = "aws_assume_role"
)
```

### Provider interface

```go
type CredentialProvider interface {
    Type() string
    URIProvider() string
    ParseConfig(agent string, section json.RawMessage) (any, error)
    PrepareMint(params json.RawMessage, config any) (cacheKey string, err error)
    Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error)
}
```

Providers own:
- the schema of their per-agent policy section (`ParseConfig`, called at broker startup and on policy reload, fails fast on missing or invalid config);
- request-time invariants such as permission subset enforcement (`PrepareMint`, returns an error before any upstream call);
- the cache key contribution that uniquely fingerprints a successful mint (`PrepareMint` again, returning the stable string).

GitHub becomes `internal/broker/providers/github`. Signer stays under `internal/broker/` because JWT signing is reusable across any provider that uses JWTs (GCP service accounts, etc.), not just GitHub.

### Policy schema v2

Each agent declares `resources` plus a credential-generic `providers` map keyed by URI prefix. Adding AWS later is a new `providers/aws` package plus a new `providers.aws` section in policy, with no policy package change.

```yaml
schema_version: "2"
default_session_ttl: "1h"
default_idle_timeout: "15m"
agents:
  claude:
    resources:
      - "github:repo:example-org/example-repo"
    providers:
      github:
        installation_id: 12345
        default_permissions:
          contents: "write"
          pull_requests: "write"
```

v1 policies are rejected with a clear error. No migration helper — pre-1.0, no installed base.

## Consequences

**Gains:**
- Adding AWS/Anthropic/registry credential support is a new provider file + policy section, not an API rewrite.
- Clear ownership of provider quirks: GitHub-isms stay inside `providers/github`.
- Policy schema reads symmetrically across credential types.

**Costs:**
- Wire-incompatible with anything that called the old API. Acceptable — nothing external calls it yet.
- Two layers of `json.RawMessage` (params, credential) for type-safety inside providers. Tradeoff for extensibility without generics gymnastics.

## Out of scope

- Provider registry / plugin loading at runtime. Compile-time registration is sufficient for the near term.
- Multi-resource sessions surfaced through the launcher CLI. Wire supports it; UX work comes when needed.
- Credential type *negotiation* (client asks "what's available for this session?"). Static for now.
