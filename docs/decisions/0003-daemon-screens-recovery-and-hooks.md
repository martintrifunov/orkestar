# ADR 0003: Daemon screens, durable metadata and interactive hooks

- Status: accepted
- Date: 2026-09-05
- Supersedes ADR 0002's client emulator, raw replay and single-pane limits

## Decision

Keep the Go daemon and Bubble Tea client. Move the isolated VT emulator and
terminal-response pump into the daemon, with one instance per PTY. Publish
complete frames over additive IPC v1 fields/events and coalesce slow-client
updates. Permit multiple viewers with exactly one input/resize controller.
Retain legacy attachment envelopes with canonical ANSI repaint content. Keep
2,000 scrollback lines in memory and expose a read-only history request.

Use `modernc.org/sqlite` behind `internal/store` for a versioned metadata
snapshot, atomically saved with WAL and synchronous FULL. This pure-Go driver
preserves the single-executable build without requiring cgo. A snapshot fits the
current metadata volume; separate relational tables/migrations can follow if
querying or write volume warrants them. Do not persist PTY bytes, submitted
prompts, environments, or ephemeral hook credentials. Explicit artifacts remain
durable. Restart restores metadata, expires leases/permissions, and marks
previously active records interrupted. Native resume is explicit and creates a
new local session; it never claims to resurrect the old PTY.

Add Codex alongside Claude Code and OpenCode. Integrate interactive lifecycle
through invocation-local Claude/Codex hooks and an OpenCode plugin. The private
hook CLI uses an ephemeral per-agent token over the local socket. Permission
requests have real reply channels and native decision encoding; unsupported
requests must be handled in the agent UI. Preserve native approval and hook-trust
policies. Global agent configuration is not modified.

Show up to four panes with grid/stacked layouts and mouse focus. Terminal input,
query ownership, persistence and approvals stay independent of layout.

## Consequences

- Closing every client leaves work and terminal state intact.
- Reattachment cannot repeat child terminal queries or start mid-escape sequence.
- Full frames favor correctness over bandwidth; profile before adding deltas.
- Daemon restart preserves orchestration metadata, but loses live screens/history.
- Hook availability and trust affect structured signals and native resume IDs.
- Existing daemons must be deliberately restarted to run the new protocol behavior;
  replacing the executable alone does not replace a running daemon.
- macOS/Linux remain the supported PTY targets; Windows is future work behind
  `internal/pty`. Unix session/group setup and descriptor ownership are explicit.

## Integration references

- [Claude Code hooks](https://code.claude.com/docs/en/hooks)
- [Codex hooks and trust](https://learn.chatgpt.com/docs/hooks)
- [OpenCode plugins](https://opencode.ai/docs/plugins/)
- [OpenCode v1.18.29 permission SDK](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/sdk/js/src/v2/gen/sdk.gen.ts)
- [SQLite driver](https://pkg.go.dev/modernc.org/sqlite)
