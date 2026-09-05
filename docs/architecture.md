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
          arbitrary commands       Claude / Codex / OpenCode      engine MCP servers
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
- Stop and forget individual sessions and agents on request. A running record
  must be stopped before it can be removed, and removing an agent takes its
  bridged terminal with it, so no record is left pointing at a missing one.
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

Claude Code and Codex use interactive PTYs. A future Claude managed mode may
consume streaming JSON or an SDK bridge. OpenCode supports both: an
interactive PTY the same way Claude Code does, and a managed mode over its
local HTTP server for structured replies (used by the reviewer-agent
workflow). All three interactive adapters share one PTY-backed Session
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

`internal/store` provides a small metadata interface implemented with SQLite
(`modernc.org/sqlite`, pure Go). A versioned JSON snapshot is atomically upserted
in `metadata.db` beside the daemon socket, using WAL and synchronous FULL.
Workspace, task/dependency/assignment, artifact, terminal and agent metadata are
durable. Successful mutations are saved before the IPC response; lifecycle
changes are also saved. Output, prompt submissions, environment variables and
hook tokens are not part of that snapshot. Explicit artifact content is durable.
Leases and pending permission channels expire when their daemon disappears.

Client reattachment retains the live process, authoritative screen and bounded
scrollback. Daemon restart restores metadata and marks previously active agents
and terminals interrupted. It never automatically restarts a command. Native
resume is explicit (`agent.resume`, CLI `agent resume`, or `u` in Agents), creates
a new local agent/terminal ID and passes the saved native session ID to the
adapter. No native ID means no resume; launch a new agent instead. PTY output and
screens are currently memory-only and are unavailable after daemon restart.

## Dependency boundaries

Bubble Tea is confined to `internal/tui`. PTY and virtual-terminal libraries
are confined to `internal/pty` and `internal/terminal`. Syntax lexing is
confined to `internal/syntax`, which returns rune offsets and hex colors so no
lexer type reaches the renderer. See
[ADR 0006](decisions/0006-syntax-highlighting.md). Domain packages consume
small Orkestar-owned interfaces so dependencies can be replaced without broad
rewrites.

## Embedded terminal rendering

The dashboard keeps Workspaces, Sessions, Tasks, Agents and permissions in the
left sidebar. Attached terminals appear beside it in a binary split tree: each
leaf is a pane, each internal node halves its rectangle side by side or stacked.
`Ctrl+b v`/`s` split the focused leaf and open a new daemon-owned shell there;
other new panes split the focused leaf along its longer edge. Closing a leaf
collapses its parent onto the sibling. The tree is the single source for pane
rectangles, rendering joins, cursor placement, mouse hit testing and PTY sizes.
An optional file viewer mirrors the sidebar on the right edge, taking its width
from the same content area calculation so pane rectangles, mouse hit testing and
PTY sizes all follow. It is closed by default and polls the workspace only while
open. A configurable limit (default 16, `max_panes` in `tui.json`) refuses further
panes instead of replacing one. A window too small for every leaf shows only the
focused pane; hidden attachments remain alive and cycle with F6. Mouse clicks and
`Ctrl+b o` change focus. Picker, read-only scrollback and prompt overlays occupy
the content area. Git review and standard file editing have their own persistent
panes; native Vim/Nano use daemon-owned terminals. The standard editor colors
source and configuration files, lexing in the background so typing never waits
for it. See
[ADR 0004](decisions/0004-review-and-editor-panes.md) and
[ADR 0005](decisions/0005-nested-split-panes.md).

The daemon owns one `internal/terminal.Screen` per PTY and starts its reply pump
before consuming any output. This screen preserves terminal modes, handles
queries exactly once even without clients, and keeps 2,000 scrollback lines.
Keyboard input, negotiated navigation and bracketed paste pass through the same
serialized screen/input path. PTY writes have deadlines, process groups receive
bounded shutdown signals, and normal exits drain final output before closing
the master. Closing a pane only releases the client socket.

`terminal.attach` accepts additive `screen` and `observe` fields. Screen clients
receive complete `terminal.screen` frames (styled content, dimensions, cursor
and revision). Each subscriber has one coalescing slot; a slow client receives
the latest complete frame rather than a damaged partial replay. Clients render
these frames without creating VT emulators or responding to child queries.
Legacy attachments receive canonical ANSI repaint output in the existing
`replay`/`terminal.output` envelopes; those replays contain no terminal queries.

A terminal has at most one input/resize controller. Other screen clients attach
as viewers. `claim`/`terminal.control` allow a viewer to claim released control;
an active controller cannot be displaced. `input`, `paste`, `key` and `resize`
commands are accepted only from the controller. `terminal.history` returns
bounded history and the current frame. All additions remain within IPC v1.

## Interactive agent signals and permissions

Claude Code and Codex receive invocation-local command hooks; OpenCode receives
an invocation-local plugin through merged `OPENCODE_CONFIG_CONTENT`. Global
agent settings are not edited. The private `orkestar hook` subprocess forwards
sanitized lifecycle identifiers to `agent.hook` using an ephemeral per-launch
token. Native IDs become resumable metadata. Agent hooks own turn state once
observed; process exit remains authoritative and late hooks cannot revive it.

A permission hook creates an in-memory inbox entry and waits for an allow/deny
reply. The CLI returns the documented hook decision JSON; the OpenCode plugin
calls its native permission API. There is no text-prompt approval fallback.
Native OpenCode replies, completed turns, process exit and shutdown release
pending hooks. Missing/expired channels fall back to the agent's native prompt.
No approval policy or hook-trust decision is bypassed. Codex users may need to
review the five Orkestar hooks at first launch; disabled or untrusted hooks leave
basic PTY/process behavior available, with limited lifecycle/resume information.

See [ADR 0003](decisions/0003-daemon-screens-recovery-and-hooks.md) for the durable
boundaries and [validation](validation.md) for what has actually been exercised.
