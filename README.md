# Orkestar

Orkestar is a local-first runtime and terminal UI for coordinating coding
agents and the tools they operate.

The background daemon owns interactive sessions so work continues when the UI
disconnects. The TUI provides one place to see agents, tasks, approvals, logs,
and development-tool integrations. Claude Code and OpenCode are the first agent
targets; game-engine workflows will integrate through MCP.

## Status

Orkestar has a working Go daemon/TUI slice with interactive agent panes and
client reattachment. Workspaces, sessions, tasks and agents stay in a left
sidebar; a selected shell or agent runs in the adjacent terminal pane.
Processes survive closing the UI while the daemon remains alive. Metadata is
currently in memory; daemon-restart recovery is not implemented.

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
go build -o /tmp/orkestar ./cmd/orkestar
/tmp/orkestar
```

- `a`: choose and launch an installed agent; `n`: launch a shell.
- `Tab`: switch sidebar section; arrows select; `Enter`: open a session/agent.
- In a pane, `Ctrl+b`, then `Tab`: focus the sidebar; `Esc`: return to the pane.
- `Ctrl+b`, then `q`: close the pane without stopping its process.
- `q` in the sidebar: quit the UI. Reopen and select the session to reattach.
- In Tasks, `d` opens the diff and `m` marks done; in Agents, `y`/`x` resolves
  an available permission request.

The UI currently shows one terminal pane at a time and needs at least 50 × 16
terminal cells. Direct full-screen attachment is also available with
`orkestar terminal attach <terminal-id>`.

Standard checks:

```bash
go test ./...
go vet ./...
```

The repository may contain local commits during development. Nothing is pushed
to `origin` without explicit user permission.
