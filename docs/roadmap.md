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
- [x] Terminal-query reply pump and real-PTY TUI regression tests

## M2: Agent awareness

- [x] Agent adapter contract and capability model
- [x] Claude Code interactive adapter
- [x] OpenCode server adapter
- [x] Lifecycle and attention contracts
- [ ] Structured interactive Claude/OpenCode working and permission signals
- [x] OpenCode managed native identity and resume
- [ ] Interactive agent native identity and resume
- [x] Permission inbox model, IPC and TUI
- [ ] Live interactive permission integration

## M3: Tasks and review

- [x] Tasks, dependencies, and assignments
- [x] Git worktree association
- [x] Diff and changed-file TUI
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

M1 has a working daemon/PTY/client vertical slice and now embeds interactive
agents alongside navigation. Tests cover capability queries and a real TUI in
an outer PTY, not only shell output. One pane is visible at a time; selecting
another session switches it while daemon processes continue.

M2 is partial: interactive adapters launch, forward input, and report process
lifecycle, but they do not yet infer real agent turn/attention state or capture
interactive native session IDs. OpenCode managed sessions provide richer
structured behavior. The permission inbox infrastructure is not proof that
interactive agent permission prompts are integrated.

M3 has task/dependency/assignment, worktree/diff, artifact, review, and lease
implementations with tests. These and workspace/agent metadata remain in memory;
client reattachment works, daemon-restart recovery does not. M4 contains the
orchestration server only. No engine integration has been implemented.

Next priorities before expanding integrations:

1. Expand the installed-agent smoke coverage (Claude Code v2.1.259 passed
   startup, unsent typing and sidebar focus) to OpenCode and full workflows;
   retain deterministic query/input fixtures for regression coverage.
2. Move authoritative VT screen/replay and terminal-response ownership into the
   daemon, including multiple-client arbitration and bounded scrollback.
3. Add durable metadata and explicit restart/resume behavior.
4. Finish structured interactive lifecycle and permission integration.
5. Add split layouts and mouse focus after the single-pane lifecycle is stable.
