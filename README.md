# Orkestar

Orkestar is a local-first runtime and terminal UI for coordinating coding
agents and the tools they operate.

The background daemon owns interactive sessions so work continues when the UI
disconnects. The TUI provides one place to see agents, tasks, approvals, logs,
and development-tool integrations. Claude Code, Codex and OpenCode are supported agent
targets; game-engine workflows will integrate through MCP.

## Status

Orkestar has a Go daemon/TUI with nested split panes (16 by default) beside a
persistent left sidebar. Processes and terminal state survive closing the UI.
SQLite preserves workspaces, tasks, artifacts and session metadata across daemon
restart; native agent resume is explicit. Claude Code, Codex and OpenCode support
interactive launch, lifecycle hooks/plugins and native permission replies.

See:

- [Architecture](docs/architecture.md)
- [Roadmap](docs/roadmap.md)
- [Architecture decisions](docs/decisions/)
- [Agent guide](AGENTS.md)

## Product principles

- Local-first and terminal-native
- Persistent work independent of any client UI
- Structured agent integrations with universal PTY fallback
- Tasks and attention states above raw process status
- Safe, serialized access to mutable game-engine editors
- One executable where practical

## Development

Build and run from a project directory:

```bash
go build -o ./orkestar ./cmd/orkestar
./orkestar
```

- `a`: choose and launch an installed agent; `n`: launch a shell.
- `Tab`: switch sidebar section; arrows select; `Enter`: open a session/agent.
- In a pane, `Ctrl+b`, then `Tab`: focus the sidebar; `Esc`: return to the pane.
- `Ctrl+b`, then `v`: open a shell beside the focused pane; `s`: open one below
  it. Every split creates a new pane; splits nest. `o` (or `F6`): next pane.
  Each action needs its own prefix.
- `Ctrl+b`, then `d`: review changes; `e`: edit a file; comma: editor settings.
  Choose standard keyboard/mouse editing, native Vim, native Nano, or a custom command.
- `Ctrl+b`, then `a`: another agent; `n`: another shell.
- Click a pane to focus it. Up to 16 panes are open at once (`max_panes` in
  `tui.json`); beyond that, new panes are refused until one is closed. Nothing
  is ever replaced silently.
- `Ctrl+b`, then `[`: scrollback; wheel or Page Up/Down scrolls; `Esc` returns.
- `Ctrl+b`, then `t`: claim input if another client's controller has disconnected.
- `Ctrl+b`, then `q`: close the pane without stopping its process.
- `q` in the sidebar: quit the UI. Reopen and select the session to reattach.
- In Tasks, `d` opens the diff and `m` marks done; in Agents, `y`/`x` resolves
  an available native permission request. `u` explicitly resumes an inactive agent.

The UI needs at least 50 × 16 terminal cells. Narrow windows show the focused
pane; wider windows show the split layout. Direct full-screen attachment is also available with
`orkestar terminal attach <terminal-id>`.

Codex may ask you to review Orkestar's five command hooks at first launch. Hooks
are configured for that invocation and do not bypass native trust or approvals.
If hooks are disabled or untrusted, the PTY still works but structured lifecycle
and native resume identity may be unavailable.

`./orkestar agent list`, `agent launch <workspace-id> <adapter>` and
`agent resume <agent-id>` also expose agent management from the CLI.

After rebuilding, an already running daemon continues using its old code. Stop
it with `./orkestar daemon stop` when its running work can end, then reopen
`./orkestar`. The previous in-memory daemon cannot migrate its live sessions into
the new store. From this version onward metadata persists; screens and scrollback
remain in memory and do not survive daemon restart.

Standard checks:

```bash
go test ./...
go vet ./...
go test -race ./...
```

Installed-agent smoke checks: `python3 scripts/smoke-agents.py` (isolated daemon,
no submitted model prompts). See [validation](docs/validation.md) for coverage.

The repository may contain local commits during development. Nothing is pushed
to `origin` without explicit user permission.

See [panes and editing](docs/panes-and-editing.md) for review, save, selection,
mouse and editor configuration shortcuts.
