<pre align="center">
 ██████╗ ██████╗ ██╗  ██╗███████╗███████╗████████╗ █████╗ ██████╗ 
██╔═══██╗██╔══██╗██║ ██╔╝██╔════╝██╔════╝╚══██╔══╝██╔══██╗██╔══██╗
██║   ██║██████╔╝█████╔╝ █████╗  ███████╗   ██║   ███████║██████╔╝
██║   ██║██╔══██╗██╔═██╗ ██╔══╝  ╚════██║   ██║   ██╔══██║██╔══██╗
╚██████╔╝██║  ██║██║  ██╗███████╗███████║   ██║   ██║  ██║██║  ██║
 ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝╚══════╝╚══════╝   ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝
</pre>

<p align="center">
  <strong>Run your coding agents from one terminal, and keep them running when you close it.</strong>
</p>

<p align="center">
  <a href="https://github.com/martintrifunov/orkestar/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/martintrifunov/orkestar/ci.yml?style=flat-square&label=build&color=D7A84B" alt="Build status"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.27-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.27"></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-D7A84B?style=flat-square" alt="macOS and Linux">
  <img src="https://img.shields.io/badge/agents-Claude%20Code%20%C2%B7%20Codex%20%C2%B7%20OpenCode-D7A84B?style=flat-square" alt="Supported agents">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/martintrifunov/orkestar?style=flat-square&color=D7A84B" alt="MIT license"></a>
</p>

Orkestar is a background daemon that owns your agent sessions, and a terminal UI
that attaches to it. Because the daemon owns the processes, closing the UI,
dropping a connection or restarting your editor does not stop the work. Reopen
it and you are back exactly where you were.

No account, no server, no browser. One static binary.

## Highlights

- **Work survives the UI.** A daemon owns every pseudoterminal, screen and agent
  process. Clients are disposable and any number can watch the same session.
- **Nested split panes.** Tile shells, agents, diffs and editors up to sixteen
  panes, with drag-resizable dividers, a zoom toggle and per-pane input.
- **A live file viewer.** A tree of the workspace on the right edge that follows
  files as agents create and delete them.
- **Review and edit in place.** A pull-request style diff pane, and an editor
  with syntax highlighting for around three hundred languages.
- **Real agent integration.** Lifecycle events, native permission prompts
  answered from the sidebar, and explicit session resume, through each CLI's own
  hook or plugin mechanism and without touching your global config.
- **Tasks with worktrees.** Units of work with dependencies, an assignee, their
  own Git worktree and an optional reviewer-agent gate before they can close.
- **Durable state.** SQLite keeps workspaces, tasks, artifacts and session
  metadata across daemon restarts.

## Installation

Build from source. Go 1.27 or newer, macOS or Linux:

```bash
git clone https://github.com/martintrifunov/orkestar
cd orkestar
go build -o ./orkestar ./cmd/orkestar
```

Install the agent CLIs you want to drive: `claude`, `codex` or `opencode`.
Orkestar works without them, as a persistent terminal multiplexer.

## Quick start

Run it from the project you want to work in:

```bash
./orkestar
```

It starts a daemon if one is not already running. Press `a` to launch an agent
or `n` for a shell. Press `q` to leave; the agent keeps working. Start Orkestar
again and press `Enter` on the session to reattach.

The UI needs a terminal of at least 50 × 16 cells.

## The interface

A sidebar on the left lists workspaces, sessions, tasks, agents and pending
permission requests. The rest of the window is a tree of panes. An optional file
viewer mirrors the sidebar on the right.

### Sidebar

| Key | Action |
| --- | --- |
| `a` / `n` | Launch an agent / open a shell |
| `Tab` | Next section |
| `↑` `↓` | Move the selection |
| `Enter` | Open the selected session or agent |
| `f` | Show or hide the file viewer |
| `u` | Resume an inactive agent |
| `i` | Interrupt an agent's current turn |
| `X` | Stop what is selected, or clear it once finished |
| `y` / `x` | Allow or deny a pending permission request |
| `r` | Refresh |
| `q` | Quit the UI |

Stopping something still running asks for a second `X`, because it ends work the
daemon is holding for you. Clearing a finished entry is immediate.

### Panes

Press and release `Ctrl+b`, then the action key. Each action needs its own
prefix, so ordinary letters still reach the agent you are typing to.

| Key | Action |
| --- | --- |
| `v` / `s` | Split: new shell beside / below the focused pane |
| `o` or `F6` | Focus the next pane |
| `z` | Zoom the focused pane to fill the area, and back |
| arrows | Move the enclosing split's divider, repeatable |
| `d` / `e` | Review changes / open a file |
| `f` | Show or hide the file viewer |
| `[` | Scrollback, 2,000 lines |
| `,` | Editor settings |
| `t` | Claim input after another client disconnected |
| `q` / `x` | Close the pane / discard an unsaved editor |

The arrows repeat: the prefix stays armed so a divider can be moved with
several presses, until `Esc` or any other key. Opening a file from the viewer
puts the editor below the focused pane, at full width.

Splits nest: `Ctrl+b s` then `Ctrl+b v` gives three panes, not a rearranged two.
Panes are labelled in their border, dividers can be dragged, and closing a pane
leaves its process running. Click any pane to focus it. When the window is too
small for every split, the focused pane fills the space and `F6` still cycles
the hidden ones.

### Tasks

Select the Tasks section with `Tab`, then `c` creates one.

| Key | Action |
| --- | --- |
| `c` | Create a task |
| `d` | Its diff and the latest reviewer verdict |
| `m` / `x` | Mark it done / cancel it |
| `w` | Create or remove its Git worktree |
| `t` | Assign it to the highlighted agent |

A task carries a status, optional dependencies, an assignee and its own Git
worktree, so an agent can change files without disturbing your checkout. Tasks
are created with automatic review on: the daemon runs a reviewer agent and
requires approval before the task can close. Press `Tab` in the create prompt to
opt out.

## Editing and review

`Ctrl+b d` opens a pull-request style review of the workspace or task worktree,
with changed-file selection, colored hunks and old and new line numbers.
`Ctrl+b e` finds or creates a file.

The built-in editor handles UTF-8 files up to 1 MiB with mouse selection, undo
and redo, search, clipboard access and conflict-checked saves that never
overwrite a change an agent made underneath you. It highlights around three
hundred languages in Orkestar's own palette, lexing in the background so typing
never waits. Vim, Nano and custom editor commands run as real daemon-owned
terminals instead.

`Ctrl+b f` shows a file tree of the workspace on the right edge. It is hidden by
default and reads nothing until opened. While open it follows the workspace, so
files an agent writes appear on their own.

## Configuration

Settings live in `orkestar/tui.json` inside your OS configuration directory, or
at `ORKESTAR_TUI_CONFIG`. The settings panel (`Ctrl+b`, comma) shows the path.

```json
{
  "editor": "custom",
  "command": ["nvim", "-c", "set mouse=a"],
  "max_panes": 16,
  "syntax": true
}
```

`ORKESTAR_RUNTIME_DIR` relocates the daemon socket and database.

## How it works

The daemon is the authority for state and process ownership. It holds each
pseudoterminal, one terminal screen per session with bounded scrollback, and the
agent adapters. Clients speak a versioned local protocol over a Unix socket and
render frames the daemon sends them; they never own a managed process. Several
clients can watch one session, but only one holds input at a time.

Claude Code, Codex and OpenCode launch interactively through a pseudoterminal.
OpenCode also has a managed HTTP mode used by the reviewer workflow. Lifecycle
signals and permission prompts arrive through invocation-local hooks or a
plugin, so no global agent configuration is edited and no native trust or
approval decision is bypassed. Codex may ask you to review Orkestar's five
command hooks the first time you launch it; declining leaves the terminal fully
working, with less lifecycle detail.

For the boundaries and the reasoning behind them, see
[the architecture](docs/architecture.md) and the
[decision records](docs/decisions/).

## Command line

```
orkestar                                          start the UI
orkestar status                                   show daemon state
orkestar daemon serve | stop
orkestar workspace create [directory]
orkestar terminal start <workspace-id> -- <cmd>   run a command in a PTY
orkestar terminal attach | stop | remove <terminal-id>
orkestar agent list | launch <workspace-id> <adapter>
orkestar agent resume | stop | remove | interrupt <agent-id>
orkestar task create | list | status | assign | worktree | diff
orkestar mcp serve                                orchestration MCP server
```

Rebuilding the binary does not upgrade a daemon that is already running. Stop it
with `orkestar daemon stop` once its work can end, then start Orkestar again.
Metadata survives a restart; terminal screens and scrollback are held in memory
and do not.

## Development

```bash
go test ./...
go vet ./...
go test -race ./...
```

Every push and pull request runs the same checks on macOS and Linux, after a
formatting check, plus a cross-build of every supported target. The suite needs
no network and no installed agent CLIs: it starts real daemons on temporary
sockets and drives real pseudoterminals with fixture commands.

Two scripts exercise installed CLIs against an isolated temporary daemon, never
your running one. `scripts/smoke-agents.py` checks each one starts and
reattaches without submitting a prompt. `scripts/live-agents.py` drives an
authenticated CLI through a real turn, permission allow and deny, interrupt and
resume; it spends tokens, so run it deliberately.
[docs/validation.md](docs/validation.md) records what has actually been
exercised.

## Documentation

- [Architecture](docs/architecture.md)
- [Panes and editing](docs/panes-and-editing.md)
- [Validation](docs/validation.md)
- [Roadmap](docs/roadmap.md)
- [Decision records](docs/decisions/)
- [Agent guide](AGENTS.md), for contributors and coding agents

## Status

The daemon, protocol, pseudoterminal ownership, persistence, panes, review,
editing, tasks and the three agent adapters are implemented and tested.
Authenticated live model turns across every installed CLI, and the MCP client
registry that game-engine work depends on, are the next milestones. The
[roadmap](docs/roadmap.md) tracks what is done and what is not.

## License

Released under the [MIT License](LICENSE).
