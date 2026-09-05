# ADR 0002: Embedded terminal panes

- Status: accepted; supersedes the interim agent rollback
- Screen ownership, replay and single-pane limits superseded by [ADR 0003](0003-daemon-screens-recovery-and-hooks.md)
- Original date: 2026-09-03
- Reassessed: 2026-09-05

## Context

The first dashboard handed the real TTY to `orkestar terminal attach` via
`tea.ExecProcess`. The user requested an embedded agent terminal alongside
persistent navigation, inspired by [Herdr](https://github.com/herdrdev/herdr).
The initial pane implementation worked for shells but froze with rich agent
CLIs, so the last session temporarily restored full-screen attachment for
agents. That rollback did not satisfy the requested interaction.

The prior record described Herdr as a native GUI and called the freeze a
confirmed upstream defect. Those conclusions were too strong. The linked
Herdr repository is a Rust terminal application. Its
[terminal implementation](https://github.com/herdrdev/herdr/blob/master/src/pane/terminal.rs)
explicitly produces terminal responses, and its
[sidebar renderer](https://github.com/herdrdev/herdr/blob/master/src/client/shell/sidebar.rs)
keeps navigation separate from terminal content. We use these architectural
ideas without adopting its language or terminal engine.

## Cause and decision

The pinned `x/vt` uses an unbuffered `io.Pipe` for terminal replies. A capability
query can synchronously write to that pipe from `SafeEmulator.Write` while
holding the screen lock. The original integration never drained the pipe;
`Render` then waited for the same lock. A minimal reproduction outside Orkestar
would encounter the same deadlock if it also omitted the reader. A missing
response pump in the integration explains this freeze without requiring a
Bubble Tea rewrite or a dependency upgrade.

- Embed shell and interactive agent terminals through the same IPC attachment.
- Keep Workspaces, Sessions, Tasks, Agents and permissions on the left, with
  one selected live pane on the right.
- Support sidebar focus without detaching the live pane: `ctrl+b tab` enters
  navigation, `esc` returns, and Enter opens the selected session or agent.
- `ctrl+b q` closes the client pane; its process remains alive. Sidebar `q`
  exits the client. `ctrl+b ctrl+b` sends a literal prefix.
- Isolate `x/vt` behind `internal/terminal.Screen`. Drain replies before replay
  and route keyboard input and negotiated paste through the same input pump.
- Bound attachment goroutines by client lifetime, coalesce repaint events,
  ignore stale events by attachment identity, and time out IPC writes.
- Keep direct CLI `terminal attach` available as a separate explicit command.

The existing Go daemon, Bubble Tea client and single-executable architecture
remain appropriate. No web/native GUI or Rust rewrite is needed for this slice.

## Validation and limits

Tests use fixture commands and the Claude adapter pointed at a fixture
executable, not installed agents. They exercise probes at startup and after
input, ordered typing, paste framing, resize, cancellation and reattachment.
A real outer PTY also drives Bubble Tea's actual input decoder and renderer,
launches an embedded agent, exits the client and reattaches to the same process.
An isolated manual smoke test also launched installed Claude Code v2.1.259,
rendered unsent text in its embedded prompt, switched to sidebar focus and
exited cleanly; no model prompt was submitted. This does not validate every
agent/version or full interactive workflows.

The renderer shows one live pane at a time. Arbitrary splits, mouse interaction,
full terminal protocol coverage, and persistent VT snapshots remain future work.
Raw replay can repeat query replies or lose state at its byte limit; response
ownership across multiple clients needs a daemon-level design before claiming
terminal-multiplexer parity. The debug log records metadata, not typed input.
