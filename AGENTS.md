# Repository guidance

Keep changes small, secure, and easy to verify.

- Protect the broker boundary. Never expose credentials or bypass brokered git and gh access.
- Read only the files and output needed for the task. Use rg or git grep before broad reads.
- Preserve unrelated work. Do not rewrite history or change remote state without a direct request.
- Respect package boundaries. Keep CLI presentation, application orchestration, broker policy, provider integrations, persistence, and telemetry transport separate. Format Go with gofmt.
- Do not add source comments. Make code self-documenting through names, types, boundaries, and small functions. Only executable language or tool directives may remain.
- Treat every security, compatibility, and lifecycle claim as incomplete until code enforces it and a focused automated check proves it. Documentation records intent; it is not enforcement.
- Give every operational tradeoff a measurable budget, emitted evidence, and a deterministic failure policy. Security and governance paths fail closed.
- Persist governance configuration and audit evidence atomically and durably. Never silently discard audit evidence.
- Do not hard-wrap prose or break text at arbitrary column widths. Keep each paragraph and list item on one source line so text remains readable and token-efficient.
- Run focused checks first. Use ai-agent check for noisy commands.
- Put stable team rules here. Put detailed procedures in docs or skills.
- Treat memory as context, not policy. Enforce important rules in code, tests, hooks, or broker policy.
- Report the result and the checks run. Do not repeat large logs.
