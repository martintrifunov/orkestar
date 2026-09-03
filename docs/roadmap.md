# Roadmap

This is a direction document, not a release-date commitment. Complete and test
each vertical slice before broadening the surface.

## M0: Repository context

- [x] Product and architecture documentation
- [x] Agent instructions
- [x] Go/TUI/daemon decision record
- [ ] Go module and automated checks

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

## M2: Agent awareness

- [x] Agent adapter contract and capability model
- [x] Claude Code interactive adapter
- [x] OpenCode server adapter
- [x] Structured lifecycle and attention reasons
- [x] Native session identity and resume metadata
- [x] Unified permission inbox

## M3: Tasks and review

- [x] Tasks, dependencies, and assignments
- [x] Git worktree association
- [x] Diff and changed-file TUI
- [x] Test/log/diff artifacts
- [ ] Reviewer-agent workflow
- [x] Resource leases

## M4: MCP gateway

- [ ] Orkestar orchestration MCP server
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
