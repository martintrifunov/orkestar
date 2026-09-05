# Orkestar

**A local-first runtime and terminal UI for coordinating coding agents.**

Orkestar runs a background daemon that owns your agent sessions, and a terminal
UI that attaches to it. Because the daemon owns the processes, closing the UI,
losing a connection, or rebooting your editor does not stop the work. Reopen the
UI and reattach exactly where you left off.

It targets Claude Code, Codex and OpenCode today, with game-engine workflows
planned through MCP.

## Highlights

- **Work survives the UI.** A daemon owns every PTY, terminal screen and agent
  process. Clients are disposable.
- **Nested split panes.** Tile shells, agents, diffs and editors side by side or
  stacked, up to 16 panes, with mouse focus and per-pane input isolation.
- **Built-in review and editing.** A PR-style diff pane and a text editor with
  syntax highlighting for roughly 300 languages, or hand off to Vim, Nano or
  your own editor.
- **Structured agent integration.** Lifecycle events, native permission
  prompts answered from the UI, and explicit session resume, using each CLI's
  own hook or plugin mechanism without touching your global settings.
- **Durable state.** SQLite keeps workspaces, tasks, artifacts and session
  metadata across daemon restarts.
- **One static binary.** Pure Go, no cgo, no external services.

## Requirements

- Go 1.27 or newer to build
- macOS or Linux
- A terminal at least 50 × 16 cells
- Optionally the agent CLIs you want to drive: `claude`, `codex`, `opencode`

## Install

```bash
git clone https://github.com/martintrifunov/orkestar
cd orkestar
go build -o ./orkestar ./cmd/orkestar
```

## Quick start

Run it from the project you want to work in:

```bash
./orkestar
```

The UI starts a daemon if one is not already running. Press `a` to launch an
agent, or `n` for a shell. Press `q` to leave; the agent keeps running. Start
Orkestar again and press `Enter` on the session to reattach.

## The interface

A persistent sidebar lists workspaces, sessions, tasks, agents and pending
permission requests. The rest of the window is a tree of panes.

### Sidebar

| Key | Action |
| --- | --- |
| `a` | Launch an agent |
| `n` | Open a shell |
| `Tab` | Next section |
| `↑` `↓` | Move the selection |
| `Enter` | Open the selected session or agent |
| `r` | Refresh |
| `u` | Resume an inactive agent |
| `y` / `x` | Allow or deny a pending permission request |
| `q` | Quit the UI |

With **Tasks** selected:

| Key | Action |
| --- | --- |
| `c` | Create a task |
| `d` | Its diff and the latest reviewer verdict |
| `m` | Mark it done |
| `x` | Cancel it |
| `w` | Create or remove its Git worktree |
| `t` | Assign it to the highlighted agent |

A task is a unit of work with a status, optional dependencies, an optional
assignee and its own Git worktree, so an agent can change files without
disturbing your checkout. Tasks are created with automatic review on: the
daemon runs a reviewer agent and requires approval before the task can move to
done. Press `Tab` in the create prompt to opt out. The selected task shows a
line summarizing what still applies to it, such as being blocked by an
unfinished dependency or having no worktree yet.

### Panes

Press and release `Ctrl+b`, then the action key. Each action needs its own
prefix, so ordinary letters still reach the agent you are typing to.

| Key | Action |
| --- | --- |
| `v` | Split: new shell beside the focused pane |
| `s` | Split: new shell below the focused pane |
| `o` or `F6` | Focus the next pane |
| `n` / `a` | New shell / new agent |
| `d` | Review changes |
| `e` | Open or create a file |
| `,` | Editor settings |
| `f` | Show or hide the file viewer |
| `[` | Scrollback (2,000 lines) |
| `t` | Claim input after another client disconnected |
| `Tab` | Focus the sidebar |
| `q` | Close the pane, leaving its process running |
| `x` | Discard an editor with unsaved changes |

### File viewer

`Ctrl+b f` shows a file tree of the workspace on the right edge, mirroring the
sidebar. It is hidden by default and only reads the workspace while open, so it
costs nothing when you are not using it. While open it follows the workspace,
picking up files an agent creates or deletes without any keystroke. Arrow keys
move, `Enter` opens a file in an editor pane or expands a directory, and `Esc`
returns to the panes. Clicking works too. Ignored files are left out, the same
way the file picker leaves them out.

The mouse wheel scrolls the review and editor panes, the file viewer, and the
scrollback view once it is open. It never opens scrollback by itself.

Splits nest: `Ctrl+b s` then `Ctrl+b v` gives three panes, not a rearranged
two. Click any pane to focus it. When the window is too small for every split,
the focused pane fills the space and `F6` still cycles the hidden ones.

### Review and editing

`Ctrl+b d` opens a PR-style review of the workspace or task worktree, with
changed-file selection, colored hunks and old and new line numbers. `Ctrl+b e`
finds or creates a file.

The built-in editor handles UTF-8 files up to 1 MiB with mouse selection,
undo and redo, search, clipboard access and conflict-checked saves that never
overwrite a change an agent made underneath you. It highlights around 300
languages in Orkestar's own palette, lexing in the background so typing never
waits. Vim, Nano and custom editor commands run as real daemon-owned terminals
instead.

See [panes and editing](docs/panes-and-editing.md) for the full reference.

## Configuration

Editor settings live in `orkestar/tui.json` inside your OS configuration
directory, or at `ORKESTAR_TUI_CONFIG`. The settings panel (`Ctrl+b`, comma)
shows the exact path.

```json
{
  "editor": "custom",
  "command": ["nvim", "-c", "set mouse=a"],
  "max_panes": 16,
  "syntax": true
}
```

`ORKESTAR_RUNTIME_DIR` relocates the daemon socket and database.

## Agent integrations

Claude Code, Codex and OpenCode launch interactively through a PTY. OpenCode
additionally supports a managed HTTP mode used by the reviewer workflow.

Lifecycle signals and permission prompts arrive through invocation-local hooks
(Claude, Codex) or a plugin (OpenCode). Orkestar never edits your global agent
configuration and never bypasses a native trust or approval decision. Codex may
ask you to review Orkestar's five command hooks the first time you launch it. If
hooks are declined the PTY still works, but structured lifecycle events and
resume identity may be unavailable.

## Command line

```
orkestar                                          start the UI
orkestar status                                   show daemon state
orkestar daemon serve | stop                      run or stop the daemon
orkestar workspace create [directory]
orkestar terminal start <workspace-id> -- <cmd>   run a command in a PTY
orkestar terminal attach <terminal-id>            full-screen attach
orkestar agent list | launch <workspace-id> <adapter> | resume <agent-id>
orkestar task create | list | status | assign | worktree | diff
orkestar mcp serve                                orchestration MCP server
```

Rebuilding the binary does not upgrade a daemon that is already running. Stop it
with `orkestar daemon stop` once its work can end, then start Orkestar again.
Metadata persists across restarts; terminal screens and scrollback are held in
memory and do not.

## Development

```bash
go test ./...
go vet ./...
go test -race ./...
```

Two scripts check real, installed CLIs against an isolated temporary daemon,
never your running one:

- `python3 scripts/smoke-agents.py` starts and reattaches each installed CLI
  without submitting a prompt.
- `python3 scripts/live-agents.py` drives an authenticated CLI through a real
  turn, permission allow and deny, interrupt and resume. It spends tokens on
  the logged-in account, so run it deliberately.

[docs/validation.md](docs/validation.md) records what has actually been
exercised, and distinguishes fixture coverage from installed-CLI coverage.

## Documentation

- [Architecture](docs/architecture.md)
- [Panes and editing](docs/panes-and-editing.md)
- [Validation](docs/validation.md)
- [Roadmap](docs/roadmap.md)
- [Architecture decisions](docs/decisions/)
- [Agent guide](AGENTS.md) for contributors and coding agents

## Status

The daemon, IPC, PTY ownership, persistence, panes, review, editing and the
three agent adapters are implemented and tested. Authenticated live model turns
across every installed CLI, CI, and the MCP client registry are the next
milestones. The [roadmap](docs/roadmap.md) tracks what is done and what is not.

## Design principles

- Local-first and terminal-native
- Persistent work independent of any client UI
- Structured agent integrations with a universal PTY fallback
- Tasks and attention states above raw process status
- Safe, serialized access to mutable game-engine editors
- One executable where practical
