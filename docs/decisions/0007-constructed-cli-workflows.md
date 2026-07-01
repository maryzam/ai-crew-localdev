# ADR 0007: Constructed CLI Workflows

## Status

Accepted

## Context

Setup, doctor, and up combined Cobra state, prompting, workflow ordering, security checks, external operations, and mutable package-level test seams. Tests exercised branches by replacing globals, so command state leaked between cases and application behavior could not be verified independently from presentation.

## Decision

Setup governance publication lives in `internal/onboarding` and readiness evaluation lives in `internal/readiness`. The `up` command sequences concrete collaborators directly; a relay-only application layer is not a domain boundary. Host operations live in `internal/uphost`; devcontainer runtime, overlay, discovery, and argument generation live in `internal/devcontainer`. Reusable workflows accept explicit input and cohesive ports for real external boundaries. Cobra adapters own flags, prompts, text and JSON rendering, and exit mapping. `cmd/ai-agent` constructs provider services and passes them through `cli.NewRoot`; production services and command options are not stored in mutable package globals.

## Consequences

Setup and readiness validation remain testable without Cobra or subprocesses. Up sequencing is covered at the command boundary, where its presentation and ordering actually meet. Platform operations move behind a port only when a real external boundary or second adapter justifies one.
