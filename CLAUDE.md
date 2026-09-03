# Claude Code Instructions

Read `AGENTS.md` first; it is the authoritative guide for this repository.
Then read:

1. `docs/architecture.md`
2. `docs/roadmap.md`
3. Relevant records under `docs/decisions/`

Orkestar is a Go daemon plus an attachable TUI, not a web application. The
daemon must own all long-lived agent processes. Preserve interactive Claude
Code behavior through the PTY adapter; do not assume that every user will run
Claude through non-interactive SDK mode.

When adding Claude-specific integration, keep it behind `internal/agent`
contracts and support both:

- interactive PTY sessions using the installed `claude` executable;
- structured managed sessions when explicitly selected.

Never commit credentials or inspect unrelated files in the user's Claude
configuration. Do not push commits without explicit permission.
