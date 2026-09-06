# Roadmap

This is a direction document, not a release-date commitment. Complete and test
each vertical slice before broadening the surface.

## M0: Repository context

- [x] Product and architecture documentation
- [x] Agent instructions
- [x] Go/TUI/daemon decision record
- [x] Go module and local test/vet checks
- [x] CI automation

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
- [x] Stop, remove and interrupt sessions and agents from the UI and CLI
- [x] SQLite metadata and explicit daemon-restart recovery
- [x] Nested split-tree layouts with a configurable pane limit and mouse focus
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
- [x] Syntax highlighting in the standard editor, in the Orkestar palette
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

## M5: Coordination and reach

Ordered by what unblocks the most. The first four deepen orchestration, which
is what Orkestar has that a terminal multiplexer does not; the fifth makes the
promise of walking away true; the sixth is daily-use polish.

- [x] **Task and agent event subscriptions, with a blocking wait.** Every
      `task.*` method is request/response today, so an agent orchestrating
      three tasks can only poll, at the cost of a model turn per check. Add a
      task event stream beside the terminal and agent ones, and
      `task.wait`/`agent.wait` with an until condition (done, blocked,
      stopped). Expose both over MCP, not only IPC. This is what turns
      "an agent can start work" into "an agent can run a pipeline", and every
      item below composes better once it exists.
- [x] **Notifications that leave the terminal.** The bell only rings for
      someone already attached, and the moments it detects — a task finishing,
      an agent stopping with work unfinished — are by definition the ones
      nobody is watching. The detection is written and tested; it needs a
      delivery path out of the TUI.
- [x] **An agent skill document for the orchestration loop.** The MCP tools
      describe themselves one at a time and nothing teaches the shape: create a
      task, give it a worktree, start an agent on it, wait, review, complete.
      An agent will not infer it. Written after the wait primitive so it
      teaches the finished loop rather than needing a rewrite.
- [x] **Workflow templates.** Declare a set of tasks, their dependencies and
      the agents that work them, then apply it. Worth little without the wait
      primitive and worth a lot with it, which is why it sits below.
- [x] **Remote daemon attachment over SSH.** The hard part is done: the daemon
      has owned every process since M1 and clients are already disposable, so
      this is a transport problem rather than an architectural one. The largest
      piece here, and nothing else depends on it.
- [x] **Task-scoped pane groups.** Panes already belong to a task; the sidebar
      does not group them that way. The affordance other multiplexers get from
      arbitrary tabs, taken from the structure Orkestar already models.

## M6: Adoption and reach

M0 through M5 made Orkestar do the work. This milestone is about somebody
other than its author being able to use it: arriving with the wrong muscle
memory, installing it the way they install everything else, and running the
agent they already run.

Ordered by what stands between a new user and a working session.

- [x] **Configurable key bindings.** Every binding is currently fixed, and
      people arrive from tmux, zellij and vim with fingers that expect
      something else. This is the first thing a new user hits and the least
      negotiable: a tool that cannot be rebound is a tool they have to think
      about. Themes come after, and only because the palette is already
      consistent enough not to be the problem.
- [x] **`install.sh` and mise distribution.** A Homebrew tap and a PowerShell
      script leave out the way most people install a terminal tool, which is a
      curl one-liner. Cheap, and it removes the first step where someone gives
      up.
- [ ] **Cursor and Grok adapters.** The adapter contract exists and the
      lifecycle work is done, so each is reach rather than depth — but it is
      the difference between "runs what I run" and "does not".
- [ ] **Named sessions and pane renaming.** One daemon per machine assumes one
      piece of work at a time. Named sessions separate projects that should not
      share a board; renaming a pane is small and makes a crowded layout
      readable.
- [ ] **Mouse menus and right-click actions.** Discoverability for people who
      do not read a help line. Worth doing after bindings, not before: someone
      who cannot rebind will not stay long enough to find a menu.

## M7: Game engines

- [ ] Unreal Engine MCP detection and routing
- [ ] Unreal editor lease and automation artifacts
- [ ] Unity MCP instance routing
- [ ] Godot adapter evaluation and integration
- [ ] Screenshot and play/test result workflows
- [ ] **Inline image protocols.** Promoted here rather than to M6 because this
      is where it earns its place: a screenshot of an engine viewport is the
      artifact the workflow produces, and a path to a PNG in a task's artifact
      list is not the same as seeing it.
- [ ] Thin engine-native status panels if they prove useful

## Later possibilities

- Plugin distribution. Deliberately not before there are users: designing
  extension points against zero real extensions means guessing wrong and then
  supporting the guess
- Optional native desktop client, only if TUI and engine panels cannot support a
  validated workflow

## M6 — 2026-09-06

The five items promoted out of "Later possibilities" are all adoption rather
than capability, which is why they are one milestone and not scattered. M0
through M5 made Orkestar do the work; none of it made Orkestar something a
second person could pick up.

Ordering is by what stands between a new user and a working session, so
bindings come first and mouse menus last: someone who cannot rebind will not
stay long enough to find a menu. Inline images went to M7 instead, since a
screenshot only becomes the point once an engine is producing them.

M4 remains unstarted and still blocks M7. It is a gateway to other MCP
servers, which is a different job from the tools Orkestar serves itself.

## M5 progress — 2026-09-06

All six shipped the day M5 was written.

Remote attachment is ssh and only ssh: the daemon never listens on a network
and Orkestar has no credential of its own. A TCP listener with TLS and tokens
was considered and rejected — it means designing an authentication model in
exchange for what ssh already does. What is untested is ssh itself, since the
machine this was built on runs no sshd; the proxy half is covered end to end.

One thing left open deliberately: a screen frame is the whole screen rather
than a diff, about 12KB for a 120x40 pane. Frames are now offered at most
sixty times a second per subscriber, which is enough for a local socket and
for a good link. A slow one would want frame diffing, which is a much larger
change and not worth making before someone has felt the need.

Three bugs surfaced from building the rest, all found by using the thing rather
than by a test:

- `workspace.create` made a new workspace on every call while its own MCP tool
  description said "create or reuse". An agent told to call it before every
  delegation was scattering work across duplicates.
- Tasks and artifacts created in the same clock tick had no defined order, so a
  sidebar the user selects by index could reorder between refreshes.
- `agent.prompt` had never submitted anything to an interactive session, having
  ended its text with a line feed where a terminal sends a carriage return.

That is the same pattern the reassessment below describes, and it is the
argument for running `scripts/live-agents.py` before adding more surface.

## Reassessment — 2026-09-06

M0 through M3 are complete. M4 holds only the orchestration MCP server, now
carrying task and agent tools so one agent can hand work to another; the four
remaining items are a different job, Orkestar acting as a gateway to *other*
MCP servers, and none of it is started. M5 is new, M6 is the old M5 renumbered.

M5 comes from reading [herdr](https://github.com/herdrdev/herdr), an
agent-native terminal multiplexer with far more people and reach. Competing
with it on multiplexer surface — plugins, themes, tabs, graphics, a marketplace
— is a race Orkestar loses and does not need to run. What it has instead is a
workflow layer: tasks with dependencies, worktrees, a review gate, artifacts,
leases, and MCP. Everything promoted into M5 was chosen because it makes that
layer work, not because a multiplexer has it.

The one thing herdr does that Orkestar cannot is let an agent wait on another
agent. That is the first item, and it is the gap that matters: the work shipped
in v0.3.0 lets an agent start a task and follows the board as it runs, but
leaves no way to wait for the result.

Also worth recording, because it shaped this list more than any feature
comparison: three bugs found on 2026-09-06 had all been present since v0.1.0 —
a prompt that never submitted, a launch that briefed nobody, an interrupt
reported as a crash. Each passed every deterministic fixture and failed against
a real agent. The fixtures test Orkestar's machinery, not that the machinery
moves an agent. Running `scripts/live-agents.py` is still outstanding and is
the standing answer to that class of defect.

## Reassessment — 2026-09-05

All five requested implementation priorities now have working slices, including
Codex. The daemon owns terminal state and replies, multiple clients share a
screen with one controller, metadata survives restart, interactive hook/plugin
approval bridges use native replies, and the TUI supports nested split panes
with mouse focus and a configurable limit. No runtime language or GUI rewrite was needed.

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

1. Run `scripts/live-agents.py` for each authenticated CLI and record its JSON
   summary in the validation matrix. The harness exists; the runs spend real
   tokens and have not been executed. OpenCode needs `opencode auth login`.
2. Watch the first CI runs and tighten any test whose timing is too tight for a
   shared runner. The workflow builds, vets, tests and race-tests on macOS and
   Linux, and cross-builds every supported target.
3. ~~Profile full-frame rendering under sustained output.~~ Done: the daemon
   rendered and marshalled a frame for every PTY chunk, including with no
   subscriber attached, and the client repainted per frame. Frames are now
   rendered at delivery and repaints are held to one per 16ms. ~~Mouse
   selection inside a terminal pane has not shipped.~~ Dragging over a terminal
   pane now selects and copies.
4. Implement the MCP client registry, health checks, policy/audit and serialized
   mutation routing before introducing engine integrations.

## v0.1.0 distribution — requested 2026-09-05

- [x] Version command and initial changelog
- [x] Preserve Shift+Enter and make confirmed reset stop the daemon, including older versions
- [x] Windows ConPTY, named-pipe transport and PowerShell scripts
- [x] Release archives, checksums and Homebrew formula generation
- [x] Run native Windows CI
- [x] Publish v0.1.0 and install the formula in martintrifunov/homebrew-tap
- [ ] Verify `install.ps1` end to end on real Windows hardware
- [ ] Run the authenticated installed-agent matrix

v0.1.0 is published. Its archives and checksums are verified, and Homebrew
installs, tests and audits clean. Windows is covered by native CI, but the
PowerShell installer has not been run on real hardware, and no authenticated
agent turns have been recorded for this version.
