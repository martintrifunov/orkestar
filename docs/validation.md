# Validation — 2026-09-05

## Automated coverage

The standard suite does not require installed agents, authentication or engines.

- All three interactive adapters run fixture commands through daemon PTYs.
  The actual private hook CLI carries native identity and lifecycle events over
  IPC. The workflow submits a turn, denies and allows permissions, observes turn
  completion, exits and explicitly resumes the saved native identity.
- Real outer PTYs drive Bubble Tea's actual input decoder/renderer for Claude,
  Codex and OpenCode fixture adapters, including client quit and reattachment.
  Another drives `Ctrl+b s` then `Ctrl+b v` and checks the rendered frame shows
  three distinct shells nested as requested, with close collapsing one split.
- Daemon tests answer terminal queries before any client exists, reject viewer
  input, hand control to a viewer after disconnect and retain the latest screen.
- Split tests focus three live panes by mouse, type/paste into each, verify input
  isolation, close/reattach one pane, and keep history overlays read-only.
- Layout tests cover zoom giving the focused pane the exact content area while
  the others stay in the tree and remain reachable by cycling, pane labels
  appearing in the border without changing a box's size and truncating when too
  long, keyboard resizing moving a divider and staying exactly tiled under two
  hundred presses in each direction without falling below the minimum, and mouse
  dragging a divider including clamping at the edge and offering none while
  zoomed.
- Reset tests confirm the daemon refuses to clear anything without explicit
  confirmation and leaves state untouched when it does, then that a confirmed
  reset empties sessions, agents, tasks, artifacts, workspaces and permissions,
  reports the task worktrees it left behind, does not delete them from disk,
  keeps registered adapters, and leaves the daemon usable for a second reset.
- Lifecycle tests stop a real daemon session from the sidebar and confirm the
  first press only asks, the second stops it, a finished session is then cleared
  along with its pane, and the daemon refuses to remove a running one with a
  message that says to stop it first. Others confirm the confirmation is per
  target and does not follow the selection, that interrupt applies only to a
  running agent, and that clearing a finished agent needs no confirmation.
- Resize tests confirm one press moves a usable amount, that the arrows repeat
  without re-arming the prefix, that `Esc` ends the repeat without the key
  reaching a pane, and that a non-repeating action still disarms it.
- Focus tests confirm the viewer yields the keyboard to a full-area overlay so
  `Esc` closes that overlay rather than the viewer, that `Esc` then returns from
  the viewer, that opening a file moves focus to the new editor so typing edits
  it, and that the editor opens below the focused pane at full width.
- File viewer tests cover tree construction and ordering, that a closed viewer
  reads nothing and does not refresh on the tick, that opening takes exactly the
  sidebar's width from the panes and gives it back on close, that its box matches
  the sidebar's width and height while the frame stays inside the window, keyboard
  navigation including expand, collapse and stepping out to a parent, opening a
  file into an editor pane, mouse clicks and wheel bounds, and refusal in a window
  with no room. A refresh test adds and removes files and asserts the expansion set
  and cursor survive. A real outer-PTY test opens the viewer with its shortcut,
  sees a file created on disk appear without a keystroke, and closes it again.
- Task tests drive the sidebar against a real daemon and Git repository:
  creating a task from the prompt including the auto-review opt-out, listing
  it, creating its worktree, opening its diff, completing it and cancelling a
  second one. Others confirm the reviewer run is announced and cannot be
  started twice, that a rejection clears the busy state, that the detail line
  reports blocking dependencies, review, assignee and a missing worktree only
  for the selected task, that `x` still denies a permission when Agents is
  focused, and that the create prompt cancels and ignores an empty title. A
  real outer-PTY test creates a task with actual keystrokes and sees it appear.
- Scrollback tests confirm the wheel never opens the overlay over a terminal
  pane or the sidebar, that `Ctrl+b [` does open it against a real daemon
  terminal, and that the wheel then scrolls it within bounds until `Esc`.
- Split-tree tests cover mixed orientation splits through real daemon shells
  (`s` then `v` gives three distinct panes with the first unchanged), nine panes
  tiling the content area exactly with no overlap after resize, collapse onto the
  sibling on close, split targets surviving focus changes and launch failures,
  the configurable pane limit refusing rather than replacing, and narrow windows
  falling back to the focused pane while F6 still cycles hidden panes.
- Terminal/PTY tests cover negotiated paste/navigation, bounded history, final
  output/EOF and bounded input/shutdown. Store/recovery tests preserve metadata
  and expire live process state.
- OpenCode plugin tests run with Node when available. Fake SDKs check both native
  permission API shapes, ordered lifecycle events and child-session filtering.
  They do not depend on OpenCode or a provider account.

Run:

```sh
go test ./...
go vet ./...
go test -race ./...
```

`.github/workflows/ci.yml` runs exactly these on `ubuntu-latest` and
`macos-latest` for every push and pull request, after a `gofmt` check, and
cross-builds linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 with cgo
disabled. Nothing in the suite needs the network or an installed agent CLI.

The full test suite, vet and race checks passed on macOS arm64. A Linux amd64
cross-build also succeeded; Linux runtime tests were not run in this session.

## Installed-agent smoke matrix

Run `go build -o ./orkestar ./cmd/orkestar`, then
`python3 scripts/smoke-agents.py`. The script launches an isolated temporary
daemon, uses installed executables on PATH, attaches/detaches/reattaches and
shuts down only its own daemon. It does not submit a model prompt. Output reports
status/identity availability without recording terminal content or credentials.

| CLI | Version tested on macOS arm64 | Observed result |
| --- | --- | --- |
| Claude Code | 2.1.259 | Startup and reattachment; hook lifecycle/native ID present |
| Codex | 0.153.4 | Startup and reattachment; native five-hook trust review displayed |
| OpenCode | 1.18.29 | Startup and reattachment; initial prompt UI, no conversation/native ID yet |

OpenCode was fetched from its official release into a temporary tools directory
and checked against the release digest. It was not installed globally. Codex
hook trust was not bypassed or approved automatically. No global agent settings
were edited. Existing user daemon sessions were left running.

## Syntax highlighting

`internal/syntax` tests assert the expected color for keywords, comments,
strings, numbers, constants, keys and variables in Go, YAML, TOML, JSON, Bash,
Python, Rust and TypeScript; that spans stay inside their line, do not overlap
and use rune rather than byte offsets across multi-byte characters; that a
shebang is detected without an extension; and that plain text, unknown content
and oversized input return nothing rather than an error.

TUI tests cover the editor side: colors appear for each of those languages and
the status line names the language, highlighting changes color without moving
any text, cursor or click position, selection overrides syntax color, colors
stay aligned under horizontal scroll, stale results are discarded while
superseded ones schedule a fresh pass, spans from a longer version cannot
overrun a shortened line, the settings toggle clears and restores colors in
already-open buffers, and a highlighted pane still fits inside its box with no
raw tabs. The real outer-PTY editor test now opens a Go file and waits for the
language in the status line, so the background lexer is exercised through the
actual program loop.

## Live authenticated matrix

`scripts/live-agents.py` exercises what fixtures cannot: real model turns from a
logged-in CLI. It starts an isolated daemon in a temporary runtime directory and
a fresh temporary Git workspace, then runs one adapter at a time through launch,
a plain turn, a permission request denied and then allowed, an interrupt, exit
and explicit native resume that must recall the earlier turn. It records
booleans, states and timings only. Screen content, prompts and credentials are
never printed. It shuts down only its own daemon and never touches a running
one.

```sh
go build -o ./orkestar ./cmd/orkestar
python3 scripts/live-agents.py --adapters claude-code
```

This spends real tokens on the account each CLI is logged into, so it is run
deliberately rather than as part of the standard suite. A CLI showing its own
hook-trust review or an unauthenticated CLI is reported and skipped rather than
answered automatically; approve hooks natively once, then rerun.

| CLI | Logged in on this Mac | Live matrix result |
| --- | --- | --- |
| Claude Code 2.1.259 | yes (claude.ai) | not yet run |
| Codex 0.153.4 | yes (ChatGPT) | not yet run |
| OpenCode 1.18.29 | no credentials | blocked: `opencode auth login` first |

Record the run's JSON summary here once it has been executed. Until then,
authenticated live model turns, tool-permission delivery and resume remain
unproven. Fixture success is not proof that every installed CLI version or
approval policy behaves identically.
Screens and scrollback survive client reattachment, but are intentionally not
persisted across daemon restart.

## Handoff

Implementation of the five requested priorities plus Codex is present. Read
[ADR 0003](decisions/0003-daemon-screens-recovery-and-hooks.md) before changing
screen ownership, storage or hooks. The root executable is ignored by Git and
must be rebuilt when code changes. Replacing it does not upgrade a running daemon;
a deliberate daemon stop ends its managed processes. The older in-memory daemon
has no automatic migration into the new SQLite store.


## Review/editor follow-up

The split/cycle regression tests cover `Ctrl+b v/s` creating nested panes,
`Ctrl+b q` collapsing them, `Ctrl+b o`, sidebar actions and F6. A real outer-PTY test opens a file through the picker, edits Unicode text,
saves with Ctrl+S, opens its resulting Git diff, cycles panes and quits. Tests
also cover selection, undo/redo, search paste, conflict detection, file bounds,
configuration round trips and negotiated mouse reports.

Isolated installed-editor checks passed launch/edit/save for `/usr/bin/vim` and
`/usr/bin/nano` on this Mac. Vim enabled mouse reporting. The system Nano is Pico
and did not enable mouse reporting in the smoke check, even with `-m`; use the
standard editor or Vim for verified mouse editing here. Native mouse forwarding
is available for editors that negotiate it. No user files or global editor
settings were changed by these checks.
