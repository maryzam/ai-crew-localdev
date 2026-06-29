# Repository guidance

Keep changes small, secure, and easy to verify.

- Protect the broker boundary. Never expose credentials or bypass brokered git and gh access.
- Read only the files and output needed for the task. Use rg or git grep before broad reads.
- Preserve unrelated work. Do not rewrite history or change remote state without a direct request.
- Use existing package boundaries. Format Go with gofmt.
- Run focused checks first. Use ai-agent check for noisy commands.
- Put stable team rules here. Put detailed procedures in docs or skills.
- Treat memory as context, not policy. Enforce important rules in code, tests, hooks, or broker policy.
- Report the result and the checks run. Do not repeat large logs.
