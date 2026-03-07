# Review of AI Agent Authentication Architecture

Thank you for putting together this comprehensive proposal for the containerized local dev environment. The two-layer signing architecture and use of Podman-first microVM isolation are particularly well thought out to keep PEM keys completely off the container filesystem.

I have reviewed the `docs/ai-agent-auth-architecture.md` and compiled a list of findings, gaps, and recommendations for this PR:

## Findings & Recommendations

### 1. Podman Rootless File Sharing (UID Mapping & SELinux)
- **Gap**: The document mentions `-v ~/github/snowflake-songs:/workspace` and `-v /run/host-signer.sock:/run/host-signer.sock`. In Podman rootless, especially on systems with SELinux (like Fedora/RHEL), bind mounts might fail due to permission denials or require the `:Z` or `:z` flag to relabel SELinux contexts. Additionally, UID mapping between the host and container can cause files created by the agent in `/workspace` to appear as the `subuid` on the host, making them unreadable to the host user unless properly mapped using `--userns=keep-id`.
- **Recommendation**: Update the "Secrets injection" Podman command to include `--userns=keep-id` (so the developer's UID matches inside the container) and mention SELinux relabeling (`:z` for shared, `:Z` for private) if applicable to the host socket and workspace mounts.

### 2. Standardized Environment Lifecycle (Devcontainers)
- **Gap**: The proposal relies on custom `ai-agent devenv up` scripts and custom Dockerfiles. While this is fine, the larger ecosystem uses the `devcontainer.json` standard (supported by VS Code, GitHub Codespaces, IntelliJ, etc.).
- **Recommendation**: Consider providing a `devcontainer.json` definition alongside or as the primary mechanism to spin up the environment. This would automatically handle workspace mounting and IDE attachment. The `host-signer.sock` could be forwarded using devcontainer mount configurations.

### 3. Host Signer Daemon Lifecycle
- **Gap**: The architecture relies on a "Host signer" that has PEM keys in memory and listens on `/run/host-signer.sock` (or vsock). However, the document does not specify how this host signer daemon is started, managed, or secured. If the daemon crashes, the containerized agents will silently fail to authenticate.
- **Recommendation**: Add a brief section detailing the lifecycle of the host signer. Should it be a user-level systemd service (`systemctl --user enable ai-agent-signer`)? How is the developer prompted to unlock the PEM keys (e.g., passphrase prompt via pinentry) before the daemon starts?

### 4. Socket Permissions and Placement
- **Gap**: The host socket is mounted as `/run/host-signer.sock`. In many Linux distributions, `/run` requires root privileges. For rootless podman and user-level daemons, sockets are typically placed in `$XDG_RUNTIME_DIR` (e.g., `/run/user/1000/host-signer.sock`).
- **Recommendation**: Update the host-signer socket path to use `$XDG_RUNTIME_DIR` instead of `/run` to align with the rootless/user-space architecture.

### 5. API Rate Limits & Caching
- **Gap**: The trade-offs summary mentions "Always fresh (no cache by default)" for tokens. As noted in the sources, GitHub App installation tokens are subject to the App's rate limits. Frequent rapid git operations (e.g., a script doing `git fetch` in a loop, or `gh pr status` polling) could hit rate limits if a new JWT and installation token is minted every single time.
- **Recommendation**: The "no cache by default" policy is secure, but a short-lived in-memory cache (e.g., 5 minutes) in the *host signer* might be necessary to avoid rate limiting for bursty operations, especially for `gh` CLI commands which make multiple API calls.

### 6. Extraheader Fallback Prevention
- **Gap**: The shim scripts ensure `credential.helper` is used, but if a developer accidentally commits `.git/config` with an `http.extraheader` or sets `GH_TOKEN` inside their shell in the container, it could bypass the secure broker.
- **Recommendation**: The shim scripts (`shims/claude`, etc.) should explicitly `unset GH_TOKEN GITHUB_TOKEN` and perhaps strip `http.extraheader` from the local git config before executing the real CLI to guarantee the broker is used.

Overall, the architecture provides a robust defense-in-depth approach to AI agent credential management. Looking forward to seeing this implemented!
