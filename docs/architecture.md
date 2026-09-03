# Architecture

## System shape

Orkestar is a background runtime with multiple replaceable clients.

```text
                  local IPC
TUI / CLI  <-------------------->  Orkestar daemon
                                         |
                 +-----------------------+-----------------------+
                 |                       |                       |
           PTY sessions             agent adapters          MCP gateway
                 |                       |                       |
          arbitrary commands       Claude / OpenCode      engine MCP servers
```

The daemon is the authority for state and process ownership. Clients render
state, submit commands, and attach to terminal streams. A client crash or clean
exit must not affect managed processes.

## Core concepts

### Workspace

A project-oriented container with a working directory. It owns agent sessions,
tasks, terminal sessions, and integration bindings.

### Terminal session

A process attached to a pseudoterminal. The daemon owns its process, PTY,
terminal state, scrollback, input arbitration, and lifecycle.

### Agent session

A terminal or managed session associated with an agent adapter. Agent lifecycle
and task lifecycle are intentionally separate.

Suggested agent states:

```text
starting -> ready -> working -> ready
                    |   |
                    |   +-> waiting_input
                    +-----> waiting_permission
                    +-----> waiting_resource
any state ----------------> stopped | crashed
```

### Task

A unit of desired work with dependencies, acceptance criteria, assignments,
and artifacts. Tasks may outlive any individual agent session.

### Resource lease

A time-bounded shared or exclusive claim on a resource. Mutable game editors,
scenes, maps, and deployment targets require exclusive leases unless an engine
adapter explicitly establishes safe concurrency.

### Artifact

A durable reference to an output such as a diff, test result, log bundle,
screenshot, build, or review report.

## Daemon responsibilities

- Acquire a per-user/session singleton lock.
- Listen on a local Unix socket or Windows named pipe.
- Own and reap child processes and PTYs.
- Maintain terminal emulation state and bounded scrollback.
- Broadcast versioned events to attached clients.
- Persist metadata and reconstruct recoverable sessions after restart.
- Enforce permissions and resource leases.
- Host agent adapters and MCP routing.
- Shut down deliberately; client disconnect is not a shutdown signal.

## Client responsibilities

- Discover or start the daemon.
- Negotiate protocol version and capabilities.
- Subscribe to snapshots and incremental events.
- Render the workspace, terminal, task, and attention views.
- Forward terminal input and resize events.
- Never directly own a managed agent process.

## IPC

The first transport is a local stream socket with newline-delimited JSON. Each
request has an ID, method, and params. Responses repeat the request ID. Event
subscriptions remain open and emit typed event envelopes.

Example:

```json
{"id":"req_1","version":1,"method":"system.ping","params":{}}
{"id":"req_1","version":1,"result":{"status":"ok"}}
```

Terminal byte streams may begin with encoded payloads over the control
protocol. If profiling shows this to be a bottleneck, add an explicitly
negotiated binary stream without changing domain APIs.

## Agent adapters

Agent adapters expose capabilities rather than pretending every agent behaves
the same:

- launch interactively;
- launch in managed mode;
- accept a prompt;
- interrupt a turn;
- resume a native session;
- emit structured lifecycle and permission events.

Claude Code initially uses an interactive PTY. Its managed mode may consume
streaming JSON or an optional SDK bridge. OpenCode initially uses its local
HTTP server and event stream, with PTY mode as fallback.

## MCP and game engines

Orkestar eventually has two MCP roles:

1. An MCP server offering orchestration tools to agents.
2. An MCP client/proxy connecting agents to tools such as engine editors.

Every proxied mutation passes through policy, audit, and resource-lease checks.
Unreal editor calls require serialization. Unity instances must be explicitly
routed. Godot integrations must declare whether they operate on files, the
editor, or a running game.

## Persistence

Metadata belongs in SQLite through a storage interface. Terminal scrollback is
bounded and may use append-only segment files rather than database rows. Secret
material does not belong in either store.

Restart recovery has two levels:

- Client reattachment: the daemon stayed alive; the PTY remains live.
- Daemon restart: metadata is restored and agents with native resumable session
  IDs may be relaunched. A Unix PTY process cannot simply survive its owning
  daemon disappearing, so documentation and UI must distinguish these cases.

## Dependency boundaries

Bubble Tea is confined to `internal/tui`. PTY and virtual-terminal libraries
are confined to `internal/pty` and `internal/terminal`. Domain packages consume
small Orkestar-owned interfaces so dependencies can be replaced without broad
rewrites.
