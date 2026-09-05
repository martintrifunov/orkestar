# Orkestar Agent Guide

This file is the primary operating guide for coding agents working in this
repository. Read it before changing code. Then read `docs/architecture.md`,
`docs/roadmap.md`, and the relevant decision records in `docs/decisions/`.

## Product

Orkestar is a local-first runtime and terminal UI for coordinating coding
agents. A background daemon owns interactive processes, sessions, scrollback,
tasks, approvals, and integrations. The TUI and CLI are disposable clients:
closing every client must not stop managed work.

The initial integrations are Claude Code, Codex and OpenCode. Game-engine integration
will be built around MCP, beginning with Unreal Engine and later Unity and
Godot. Orkestar must remain useful for ordinary software projects without a
game engine.

## Non-negotiable architecture

- The daemon, never the TUI, owns agent processes and PTYs.
- Client/daemon communication uses a versioned local IPC protocol.
- The core implementation is Go and should ship as one executable where
  practical.
- The primary interface is a TUI. Do not add a web or desktop application
  without an explicit product decision.
- Agent adapters prefer structured lifecycle signals and retain a PTY fallback.
- MCP is the tool integration layer, not the agent lifecycle layer.
- Game-engine mutations must support exclusive resource leases; do not allow
  concurrent unsafe editor mutations.
- Wrap experimental terminal dependencies behind internal interfaces.
- Domain code must not depend on Bubble Tea or a specific PTY package.
- Use clear technical nouns (`workspace`, `agent`, `task`, `session`) in code.
  Keep the orchestral metaphor in branding and user-facing copy.

## Repository layout

The intended package boundaries are:

```text
cmd/orkestar/       executable entry point
internal/daemon/    process owner and application coordinator
internal/ipc/       versioned client/server protocol and transports
internal/pty/       platform PTY abstraction
internal/terminal/  terminal state, replay, and scrollback
internal/agent/     agent contracts and adapters
internal/workflow/  tasks, dependencies, and resource leases
internal/mcp/       MCP client, server, policy, and routing
internal/engine/    Unreal, Unity, and Godot adapters
internal/store/     durable local state
internal/tui/       Bubble Tea client
protocol/           public protocol fixtures or generated schemas
```

Create packages only when the current milestone needs them. Avoid empty
scaffolding and speculative abstractions.

## Current milestone

Build the smallest reliable vertical slice:

1. One executable can start or discover a local daemon.
2. A TUI client connects to it over local IPC.
3. The daemon can create a workspace and launch an interactive command in a
   PTY.
4. Terminal input, output, and resize flow through the daemon.
5. Closing and reopening the TUI leaves the command running and allows
   reattachment.
6. Tests cover protocol framing, lifecycle transitions, and reconnect behavior.

macOS and Linux are the first supported platforms. Preserve a clean PTY
interface so Windows ConPTY can be added without changing domain packages.

## Engineering conventions

- Prefer the Go standard library until a dependency materially reduces risk.
- Keep goroutine ownership explicit. Every long-lived goroutine needs a clear
  cancellation and shutdown path.
- Use `context.Context` for request-scoped cancellation, not as object storage.
- Put timeouts on IPC, subprocess shutdown, and external integrations.
- Errors should add operation and resource context and remain inspectable with
  `errors.Is`/`errors.As` where useful.
- Keep protocol messages backward-compatible within a protocol version.
- Never log credentials, complete environment dumps, or model prompts by
  default.
- Tests must not depend on installed Claude Code, OpenCode, or game engines.
  Use fixture commands and fake adapters.

Before committing Go changes, run:

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

## Git and scope discipline

- Preserve user changes and inspect the worktree before editing.
- Keep commits atomic: one coherent behavior or documentation change per
  commit, with its tests included.
- Use imperative commit subjects.
- Do not push to any remote without the user's explicit permission.
- Do not rewrite published history or use destructive Git commands.
- Do not silently broaden the current milestone.

## Documentation

Update architecture or decision records when changing a durable boundary,
protocol, dependency strategy, persistence model, or supported platform.
Update `docs/roadmap.md` when a milestone meaningfully advances.
