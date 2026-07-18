# User Guide

For people **running** AI Crew localdev against their own repos. Everything here assumes a pre-built release binary — you never need a source checkout.

**Start with the [User Manual](user-manual.md).** It has the 5-minute quick start, a plain-English "how it works," and the everyday commands. The rest is reference you reach for when you need it.

| Doc | What's in it |
|-----|--------------|
| [User Manual](user-manual.md) | Start here: quick start, how the broker works, everyday commands |
| [Setup](setup.md) | Install, GitHub App, `identities.json`/`policy.json`, broker service, env vars, file locations |
| [CLI Reference](cli-reference.md) | Every command and flag |
| [Using the Container](using-the-container.md) | What's in the image, project mode, agent login state, re-entering, driving it by hand |
| [Quality Gates](quality-gates.md) | Manifest contracts, verify-and-retry, home isolation, token and output budgets |
| [Observability](observability.md) | Run history, Langfuse, the advisory analyzer, findings ledger |
| [Security — What Protects You](security-for-users.md) | What the tool guarantees about your credentials, and what it does not |
| [Troubleshooting](troubleshooting.md) | Symptom → fix |

Building the tool or contributing to it? That is a different audience — see the [design docs](../design/README.md).
