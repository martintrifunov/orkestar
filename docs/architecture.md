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
streaming JSON or an optional SDK bridge. OpenCode supports both: an
interactive PTY the same way Claude Code does, and a managed mode over its
local HTTP server for structured replies (used by the reviewer-agent
workflow). Both interactive adapters share one PTY-backed Session
implementation (`internal/agent/ptysession`) so a future interactive adapter
does not reimplement PTY lifecycle, prompt/interrupt, or lifecycle-event
plumbing.

An interactive agent session is not a second, parallel notion of "terminal
output". A session whose adapter exposes the underlying PTY process
(`agent.ProcessSession`) is bridged by the daemon into the same terminal
buffer/subscriber/input machinery used by plain terminal sessions
(`terminal.start`), and gets a terminal ID a client can attach to with the
ordinary `terminal.attach` — there is one terminal-attach mechanism, not one
for plain commands and a different one for agents.

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

## Embedded terminal rendering

The dashboard keeps Workspaces, Sessions, Tasks, Agents, and permission details
in a left sidebar. One selected terminal is visible beside it. Shells and
interactive agents use the same embedded `terminal.attach` path; selecting an
agent uses that agent's `TerminalID`. Managed agents have no attachable PTY.
The picker and task diff appear in the content area while the sidebar remains.

`internal/terminal.Screen` wraps the pinned experimental `x/vt` emulator.
The TUI feeds output and replay into it and renders the resulting grid. A
separate input pump drains the emulator's reply pipe **before replay is parsed**
and sends terminal-query replies back through IPC to the daemon-owned PTY.
User input and bracketed paste use this same pump in order; application cursor
keys respect the child terminal's negotiated mode. Without the pump, a query
blocks `Write` on an unbuffered pipe while holding the emulator lock, which also
blocks rendering. This caused the previous apparent keyboard freeze.

Attachment ownership is bounded by the UI lifetime. Closing a pane, switching
sessions, or exiting the client releases its socket, emulator pipe, and read/
write goroutines. It does not stop the daemon-owned process. Repaint messages
are coalesced so the reader does not wait for rendering. Stream handshakes and
writes have deadlines. VT internals stay inside `internal/terminal`; its wrapper
avoids the upstream emulator's unsynchronized close flag by closing the
underlying concurrency-safe pipe directly.

This is one visible pane with session switching, not yet a general split tree.
The daemon still retains a bounded raw-byte replay, not an authoritative VT
screen snapshot. Truncated replay can begin mid-sequence or lose prior modes;
replaying queries can repeat replies. A daemon-owned screen and explicit
terminal-response ownership are follow-up work, especially for multiple clients.
There is no durable metadata store or daemon-restart recovery yet.
