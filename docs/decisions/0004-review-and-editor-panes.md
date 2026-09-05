# ADR 0004: Review and editor panes in the terminal client

- Status: accepted
- Date: 2026-09-05

## Decision

Keep the TUI and daemon architecture. The same pane layout (now a split tree,
see ADR 0005) can display PTY terminals, a local Git review, and a local
standard text editor. Diff and
standard editor buffers belong to the disposable client; agent processes and
native Vim/Nano editors remain daemon-owned PTYs.

Global `Ctrl+b` actions work from both terminal and sidebar focus. `v` and `s`
originally selected a flat grid or stacked layout and only created a shell when
one pane was open; ADR 0005 changed them to always split the focused pane and
open a new shell. `o` and `F6` cycle open panes.
The UI explicitly reports when there is only one pane or too little space for
splits. Ordinary letters still reach an active terminal.

The review pane shows staged/unstaged changes against HEAD and untracked files,
with changed-file selection, colored unified hunks and old/new line numbers.
It follows the active terminal's directory or selected task worktree/workspace.
It shows all workspace changes, without claiming agent-level authorship. Refresh
is explicit (`r`), so review reads do not spawn continuous Git processes.
Git calls have deadlines and bounded output and disable external diff/textconv.

The built-in standard editor supports UTF-8 files up to 1 MiB, keyboard and mouse
selection, undo/redo, search, terminal clipboard access, and explicit saves.
`internal/files` confines paths to the workspace, rejects binary files, checks
for intervening disk changes, and atomically replaces saved files while keeping
permissions. It does not automatically merge concurrent agent edits. Dirty
buffers block normal pane closing, replacement and client quit; explicit discard
is available. Closing the host terminal or killing the client can lose unsaved
standard-editor buffers. There is no draft persistence or language server yet.

Editor mode is configured in the per-user `orkestar/tui.json` (or
`ORKESTAR_TUI_CONFIG`). The settings panel chooses standard, Vim or Nano for new
file panes. Native modes invoke the real editors for their complete keymaps;
custom terminal editor commands are an argv array with the file path appended.
Native mouse input travels over additive IPC v1 `mouse` commands. The daemon
encodes reports according to the child terminal's negotiated mouse mode and
accepts them only from the controller. Frame metadata advertises mouse mode;
other terminals retain Orkestar scrollback-wheel behavior.

## Limits

This adds an editor and PR-style review to the TUI, not full IDE parity. Native
editors must be installed. On macOS, `/usr/bin/nano` may be Pico; the shared `-m`
mouse option is used. Clipboard support depends on the host terminal (OSC52 or
its normal paste shortcut). Standard-editor drafts are not durable.
