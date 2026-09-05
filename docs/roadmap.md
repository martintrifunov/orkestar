# Roadmap

This is a direction document, not a release-date commitment. Complete and test
each vertical slice before broadening the surface.

## M0: Repository context

- [x] Product and architecture documentation
- [x] Agent instructions
- [x] Go/TUI/daemon decision record
- [x] Go module and local test/vet checks
- [ ] CI automation

## M1: Persistent terminal vertical slice

- [x] Single Orkestar executable
- [x] Local daemon discovery and startup
- [x] Versioned IPC ping and snapshot methods
- [x] Workspace creation
- [x] PTY-backed command launch
- [x] Terminal output subscription
- [x] Terminal input and resize
- [x] TUI workspace and terminal views
- [x] Detach and reattach without stopping the process
- [x] Graceful explicit daemon shutdown
- [x] Lifecycle and reconnect tests
- [x] Embedded shell/agent pane with persistent left navigation
- [x] Daemon-owned screen and query reply pump
- [x] Multiple-client input/resize arbitration and canonical replay
- [x] Bounded scrollback with TUI history
- [x] SQLite metadata and explicit daemon-restart recovery
- [x] Four-pane grid/stacked layouts with mouse focus
- [x] Real-PTY TUI regression tests for Claude, Codex and OpenCode fixtures

## M2: Agent awareness

- [x] Agent adapter contract and capability model
- [x] Claude Code interactive adapter
- [x] Codex interactive adapter
- [x] OpenCode server adapter
- [x] Lifecycle and attention contracts
- [x] Structured Claude/Codex hook and OpenCode plugin signals
- [x] OpenCode managed native identity and resume
- [x] Interactive native identity and explicit resume for all three adapters
- [x] Permission inbox model, IPC and TUI
- [x] Interactive permission reply channels (hooks/plugin; native trust applies)

## M3: Tasks and review

- [x] Tasks, dependencies, and assignments
- [x] Git worktree association
- [x] Persistent PR-style review pane with file selection and line numbers
- [x] Standard text editor with mouse, undo, search and conflict-checked saves
- [x] Configurable native Vim/Nano/custom terminal editor panes
- [x] Test/log/diff artifacts
- [x] Reviewer-agent workflow
- [x] Resource leases

## M4: MCP gateway

- [x] Orkestar orchestration MCP server
- [ ] MCP client registry and health checks
- [ ] Policy and audit middleware
- [ ] Per-agent tool exposure
- [ ] Serialized mutation routing

## M5: Game engines

- [ ] Unreal Engine MCP detection and routing
- [ ] Unreal editor lease and automation artifacts
- [ ] Unity MCP instance routing
- [ ] Godot adapter evaluation and integration
- [ ] Screenshot and play/test result workflows
- [ ] Thin engine-native status panels if they prove useful

## Later possibilities

- Mouse menus and right-click actions
- Rich terminal layouts and tabs
- Inline image protocols
- SSH and remote daemon attachment
- Workflow templates and plugin distribution
- Optional native desktop client, only if TUI and engine panels cannot support a
  validated workflow

## Reassessment — 2026-09-05

All five requested implementation priorities now have working slices, including
Codex. The daemon owns terminal state and replies, multiple clients share a
screen with one controller, metadata survives restart, interactive hook/plugin
approval bridges use native replies, and the TUI supports four panes with mouse
focus. No runtime language or GUI rewrite was needed.

Validation distinguishes fixtures from installed agents. Deterministic tests
exercise all three adapters through prompt, permission allow/deny, completion
and native resume. Outer-PTY tests drive the actual TUI and reconnect to the same
agent process. Other tests cover detached queries, controller handoff, final
output, bounded history, split input isolation, persistence and hook lifecycle.
Installed Claude Code, Codex and OpenCode have startup/reattachment smoke coverage;
Codex's native hook-trust review remains intact. See [validation](validation.md).

M2 has structured integration code and deterministic workflows; authenticated
live model-turn/permission/resume testing across all three CLIs remains release
validation. Hook support depends on agent version and policy. M3 metadata is now
durable, while active leases and permission channels intentionally expire on
daemon restart. M4 still contains the orchestration MCP server only. No engine
integration has been implemented.

Next priorities:

1. Exercise authenticated live turns, native approvals and resume after hook
   review for each CLI; add these results to the versioned validation matrix.
2. Add macOS/Linux CI, including race and real-PTY regression checks.
3. Profile full-frame rendering under sustained output and refine layout controls
   if needed (arbitrary split trees, pane titles, mouse selection).
4. Implement the MCP client registry, health checks, policy/audit and serialized
   mutation routing before introducing engine integrations.
