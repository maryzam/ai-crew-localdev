# Design & Contributor Docs

For people **building** AI Crew localdev or contributing to it. This track covers architecture, how the security guarantees are enforced, how to build from source, and the principles a change should optimize for. It overlaps the [user guide](../guide/README.md) in subject but not in angle: the user guide asks "how does this protect me?", this track asks "how do we enforce it, and how do we harden it further?"

| Doc | What's in it |
|-----|--------------|
| [Architecture](architecture.md) | Current and north-star architecture, domain ownership, core invariants |
| [Security Design](security-design.md) | The credential path, enforced invariants and their enforcement points, hardening roadmap |
| [Building From Source](build-from-source.md) | `make build`/`install`, binary layout, the embedded-asset contract, verify gates |
| [Design Principles](design-principles.md) | Keep the wrapper lean, keep the UX invisible, quality as a contract, and pleasant-to-live-in touches |
| [Product Gap Analysis](gap-analysis.md) | The long-lived source of truth for the gap between current product and north star |
| [Decisions (ADRs)](../decisions/) | Architecture decision records; a gate requires one for broker/policy changes |

The enforceable team rules live in [AGENTS.md](../../AGENTS.md) at the repository root.
