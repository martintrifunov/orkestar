# ADR 0002: Embedded terminal panes — interim rollback for agent sessions

- Status: interim / partially rolled back — does not reflect the desired
  end state, recorded here so the next session has full context instead of
  repeating the investigation.
- Date: 2026-09-03

## Context

The TUI originally attached a session (plain shell or launched agent) by
handing the real TTY to a subprocess (`tea.ExecProcess` running `orkestar
terminal attach <id>`), taking over the whole screen until the user
detached. The user wanted something closer to Herd's layout instead: the
agent's live output rendered as an inline pane next to Workspaces/Tasks/
Agents, never leaving the dashboard.

Herd itself turned out to be a native Rust GUI app (not a terminal UI), so
its rendering approach doesn't transfer directly — but its daemon-owns-
the-PTY architecture validated what Orkestar already had. The TUI-native
equivalent is a VT100 emulator parsing the PTY byte stream into a grid,
rendered as one pane inside Bubble Tea instead of a native window.

## What was built

- `internal/tui/embedded.go`: opens `terminal.attach` directly from the
  TUI process (no subprocess), feeds the raw PTY byte stream into
  `github.com/charmbracelet/x/vt` (an unreleased VT100 emulator from the
  Bubble Tea team), and re-renders the pane on every output event.
- `internal/tui/keyencode.go`: translates Bubble Tea's already-decoded
  `tea.KeyPressMsg` back into raw terminal bytes, since nothing downstream
  produces those anymore once Bubble Tea owns key decoding.
- Two real bugs found and fixed along the way:
  - A layout bug where the emulator grid was sized to the box's outer
    width instead of its interior, corrupting the whole layout (lipgloss's
    `Style.Width/Height` set total size including border+padding, not
    interior size).
  - A keystroke-ordering bug: each keypress sent itself to the daemon via
    its own `tea.Cmd`, and Bubble Tea gives no ordering guarantee across
    concurrently-scheduled `Cmd` goroutines, so fast typing could reach the
    daemon scrambled. Fixed by sending synchronously from `Update`.

## The blocking problem

After both fixes, typing into an embedded **Claude Code** session still
didn't work, and the ctrl+b q detach shortcut didn't either. This was
root-caused with a real pseudo-terminal driving the actual binary (typing
into it programmatically, not guessing):

- A plain shell embedded the same way handles a full typed command with no
  issue.
- Claude Code, embedded, stops receiving keys after exactly 1-2 keystrokes,
  every time.
- This was isolated **completely outside Orkestar's own code**: a ~100-line
  standalone program (no daemon, no IPC — just `vt.Emulator` feeding a
  really-spawned `claude` process's output into Bubble Tea) hits the exact
  same freeze.
- Ruled out: a panic (the process stays alive throughout), a data race (a
  `-race` build shows nothing), our own key-encoding logic (proven correct
  via the shell test), and the daemon inheriting the TUI's controlling
  terminal (it already uses `setsid` and a closed stdin correctly).
- Likely mechanism (not fully confirmed): Claude Code sends its own
  terminal-capability queries (DEC mode / Kitty keyboard protocol probes,
  visible in its output) that nothing answers once its PTY is consumed by
  `vt.Emulator` instead of a real terminal. `vt.Emulator` never writes a
  response back via its own `Read()`/`InputPipe()` — Orkestar's reader loop
  doesn't consume or forward one either. Plausible given `vt` is
  unreleased software with an already-confirmed concurrency gap (its
  `SafeEmulator` protects `Write`/`Render`/`Resize` but not `String`,
  found earlier in the same investigation).

This is very likely a bug in the `vt`/Bubble Tea stack itself, not in
Orkestar's code — but that distinction doesn't matter to the user
experience: agent sessions were unusable embedded.

## Interim decision

`Model.terminalIsAgentBridged` (checking `snapshot.Agents` for a matching
`TerminalID`) now decides which path `attachSelected` takes:

- Plain terminal sessions → embedded pane (`internal/tui/embedded.go`).
- Agent sessions (Claude Code, OpenCode) → subprocess hand-off
  (`tea.ExecProcess`, the pre-embedded-pane mechanism), unconditionally.

This was verified working (typed digits arrive and echo correctly via the
subprocess path) but **it is explicitly not what the user asked for**. The
user wants agent sessions embedded too, matching the Herd-style layout for
every session kind, not just plain shells. Reverting agents to a
full-screen subprocess was a pragmatic unblock, not the intended design —
raised directly with the user, who ended the session dissatisfied with
this outcome rather than approving it as a resting point.

## Open problem for a future session

Get an interactive agent CLI (Claude Code, and presumably OpenCode too)
rendering as an inline pane without hitting the input freeze. Directions
worth trying, roughly in order of how self-contained they are:

1. **Answer the terminal-capability queries.** Consume `vt.Emulator`'s
   `Read()`/`InputPipe()` output in the reader goroutine and forward it to
   the real PTY process, the same way a real terminal would answer DEC
   mode / Kitty protocol queries. Untested — may or may not be what
   Claude Code is actually waiting on.
2. **File or search for the bug upstream** in `charmbracelet/x/vt` (and/or
   `bubbletea`) — it's unreleased software; this may already be a known
   issue, or worth reporting with the minimal repro described above
   (available in this session's scratch history, not preserved in-repo).
3. **Re-check after upgrading** `x/vt` / `bubbletea` / `ultraviolet` — all
   pinned to specific unreleased commits; a newer commit may have already
   fixed this.
4. **Reconsider the mechanism entirely** if the above don't pan out — e.g.
   a different terminal-emulation library, or accepting that rich
   interactive agent CLIs need the subprocess hand-off permanently while
   only plain shells (and perhaps future non-interactive/managed-mode
   agents, which don't need a PTY at all) get the inline pane.

The debug hook added this session (`internal/tui/debug.go`,
`ORKESTAR_TUI_DEBUG=1` → `/tmp/orkestar-tui-debug.log`) is low-cost and
still in the tree; it's what made root-causing this tractable and is
likely to be useful again for whichever direction comes next.

## Revisit when

- Any of the four directions above produces a working embedded pane for
  Claude Code (or another interactive agent CLI).
- The user decides the subprocess hand-off is acceptable for agents
  permanently, at which point this ADR should be updated to reflect that
  as the actual decision rather than an interim state.
