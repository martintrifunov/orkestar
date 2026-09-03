# Orkestar

Orkestar is a local-first runtime and terminal UI for coordinating coding
agents and the tools they operate.

The background daemon owns interactive sessions so work continues when the UI
disconnects. The TUI provides one place to see agents, tasks, approvals, logs,
and development-tool integrations. Claude Code and OpenCode are the first agent
targets; game-engine workflows will integrate through MCP.

## Status

Orkestar is at the initial foundation stage. The first milestone is a reliable
daemon/TUI vertical slice with persistent interactive PTYs and reattachment.

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

The project targets Go. Once the initial module is present, the standard checks
are:

```bash
go test ./...
go vet ./...
```

The repository may contain local commits during development. Nothing is pushed
to `origin` without explicit user permission.
