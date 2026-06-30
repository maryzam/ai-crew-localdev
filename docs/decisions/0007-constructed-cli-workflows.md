# ADR 0007: Constructed CLI Workflows

## Status

Accepted

## Context

Setup, doctor, and up combined Cobra state, prompting, workflow ordering, security checks, external operations, and mutable package-level test seams. Tests exercised branches by replacing globals, so command state leaked between cases and application behavior could not be verified independently from presentation.

## Decision

Setup governance publication lives in `internal/onboarding`, readiness evaluation lives in `internal/readiness`, and up sequencing lives in `internal/application/up`. Concrete host operations live in `internal/uphost`; devcontainer runtime, overlay, discovery, and argument generation live in `internal/devcontainer`. Each use case accepts explicit input and cohesive ports for real external boundaries. Cobra adapters own flags, prompts, text and JSON rendering, and exit mapping. `cmd/ai-agent` constructs provider services and passes them through `cli.NewRoot`; production services and command options are not stored in mutable package globals.

## Consequences

Workflow ordering, validation, and fail-stop behavior are testable without Cobra or subprocesses. CLI acceptance tests remain responsible for exact user-visible output and generated process arguments. Platform operations remain in CLI adapters until a concrete second adapter justifies another boundary.
