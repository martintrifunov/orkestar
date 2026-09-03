# Roadmap

This is a direction document, not a release-date commitment. Complete and test
each vertical slice before broadening the surface.

## M0: Repository context

- [x] Product and architecture documentation
- [x] Agent instructions
- [x] Go/TUI/daemon decision record
- [ ] Go module and automated checks

## M1: Persistent terminal vertical slice

- [ ] Single Orkestar executable
- [ ] Local daemon discovery and startup
- [ ] Versioned IPC ping and snapshot methods
- [ ] Workspace creation
- [ ] PTY-backed command launch
- [ ] Terminal output subscription
- [ ] Terminal input and resize
- [ ] TUI workspace and terminal views
- [ ] Detach and reattach without stopping the process
- [ ] Graceful explicit daemon shutdown
- [ ] Lifecycle and reconnect tests

## M2: Agent awareness

- [ ] Agent adapter contract and capability model
- [ ] Claude Code interactive adapter
- [ ] OpenCode server adapter
- [ ] Structured lifecycle and attention reasons
- [ ] Native session identity and resume metadata
- [ ] Unified permission inbox

## M3: Tasks and review

- [ ] Tasks, dependencies, and assignments
- [ ] Git worktree association
- [ ] Diff and changed-file TUI
- [ ] Test/log/diff artifacts
- [ ] Reviewer-agent workflow
- [ ] Resource leases

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
