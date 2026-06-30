# ADR 0009: Executable Source Policy

## Status

Accepted

## Context

Incremental checking rejected only new comments inside changed Go function bodies. Existing package, declaration, shell, workflow, Docker, and configuration comments remained outside enforcement, and lint suppressions could turn unexplained exceptions into permanent policy. Security and lifecycle claims in prose were not executable evidence.

## Decision

Tracked source files contain no explanatory comments or lint suppressions. The repository checker parses Go comments and supported hash- or slash-comment source formats, rejects inline and block comments, and permits only shebangs, compiler or language directives, generated-file markers, and Docker parser directives. It checks the working tree in `make verify` and CI, the staged index before commit, and the pushed commit before branch publication.

Names, types, package boundaries, executable checks, health behavior, metrics, and tests carry implementation intent. Markdown records architecture, operations, and decisions without serving as enforcement. Prose paragraphs and list items remain on one source line unless syntax requires otherwise.

## Consequences

New source comments and `nolint` directives fail before merge. Generated and compiler-controlled inputs remain usable. Existing commentary is replaced by clearer code or durable documentation, while critical behavior requires automated verification rather than a claim beside the implementation.
