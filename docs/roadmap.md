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
- [x] **Cursor and Grok adapters.** The adapter contract exists and the
      lifecycle work is done, so each is reach rather than depth — but it is
      the difference between "runs what I run" and "does not".
- [x] **Named sessions and pane renaming.** One daemon per machine assumes one
      piece of work at a time. Named sessions separate projects that should not
      share a board; renaming a pane is small and makes a crowded layout
      readable.
- [x] **Mouse menus and right-click actions.** Discoverability for people who
      do not read a help line. Worth doing after bindings, not before: someone
      who cannot rebind will not stay long enough to find a menu.

## M6 progress — 2026-09-06

All five shipped. What each cost was mostly honesty rather than code:

- The installer verifies a checksum before unpacking and tells the user what to
  add to PATH rather than editing a shell profile. mise needs nothing from us,
  which does tie three installers to the release archive naming; that is now
  written down where the release process is.
- Cursor and Grok claim only what has been checked. Neither CLI was installed
  on the machine they were written on, so they run in a pane and report started,
  stopped or crashed, and nothing about attention. `docs/validation.md` says
  what finishing them would take.
- Configurable bindings forced the key map to be written out, which turned up
  two conflations in the old code: the prefixed x discards an editor and has
  nothing to do with cancelling a task, and deny and cancel were always one
  binding whose meaning follows the focus.

## M7: Game engines

- [ ] Unreal Engine MCP detection and routing
- [ ] Unreal editor lease and automation artifacts
- [ ] Unity MCP instance routing
- [ ] Godot adapter evaluation and integration
- [ ] Screenshot and play/test result workflows
- [x] **Inline image protocols.** Promoted here rather than to M6 because this
      is where it earns its place: a screenshot of an engine viewport is the
      artifact the workflow produces, and a path to a PNG in a task's artifact
      list is not the same as seeing it.
- [ ] Thin engine-native status panels if they prove useful

## Competitive runtime parity — 2026-09-16

A read of [herdr](https://github.com/herdrdev/herdr) 0.9.0, an agent-native
multiplexer with far more reach. Orkestar already owns the parts that matter:
a daemon that owns every PTY, detach and reattach without stopping work,
attention states, agent-to-agent waits, and a workflow layer herdr has no
equivalent of (tasks, dependencies, a review gate, artifacts, resource
leases). This track is only the runtime and daily-use surface where herdr is
ahead, excluding the MCP gateway and game engines. It is ordered by what
removes the most reasons to choose herdr; the milestones are mostly
independent, so the order can move.

### M8: Agent-native control surface

Orkestar's IPC is client-internal and its CLI controls terminals and tasks but
cannot read a terminal's output or subscribe to terminal events. herdr's entire
CLI is the agent and plugin API. Without this an agent cannot drive Orkestar
the way it drives herdr.

A note on "pane": in Orkestar the split tree is the TUI's, and each leaf
attaches to a daemon-owned terminal. So the server-side control surface is
over terminals and sessions, not panes; pane layout stays a client concern.
Where herdr has `pane.split`, Orkestar's daemon equivalent is starting another
terminal (`terminal.start`).

- [ ] Terminal and session control over IPC with CLI wrappers: start, send
      text or keys, resize, rename, stop, and remove. `terminal.start`,
      `terminal.send`, `terminal.stop` and `terminal.remove` landed 2026-09-16;
      resize happens through an attachment, and a terminal has no name to
      rename (pane labels are client-side).
- [ ] Read a terminal's output: visible, recent, and unwrapped.
      `terminal.read` and `orkestar terminal read --lines N` landed 2026-09-16
      for visible and recent output; unwrapped output remains.
- [ ] Event subscriptions for terminal, agent and workspace lifecycle, and
      extend the existing waits with output and agent-state conditions.
      `terminal.wait` on an output condition landed 2026-09-16 over IPC, CLI
      and MCP; `task.attach` streams the board over IPC and CLI. Terminal and
      agent lifecycle already stream through their attach methods. Workspace
      lifecycle events remain.
- [x] Expose the same surface over MCP so one agent can read and drive another.
      `terminal_start`, `terminal_list`, `terminal_read` and `terminal_send`
      landed 2026-09-16, and the connect instructions teach them.
- [x] `agent explain`: why Orkestar believes an agent is in its current state.
      Landed 2026-09-16 over IPC, CLI and MCP: signal source, live process,
      resumability, pending permissions, and the reasons behind the state.

First slices landed 2026-09-16: `terminal.read` and `terminal.send` over IPC,
with `orkestar terminal read <id> [--lines N]` and `orkestar terminal send
<id> [--enter] <text>`, verified against a scratch daemon. `terminal.send`
refuses while a client holds the input controller, so a script cannot race a
person typing. Acceptance for the milestone remains: an agent using only the
CLI or socket starts a terminal, runs a command, reads its output, and waits
for another agent to block.

### M9: Session continuity across daemon restart

herdr restores the screen shape after a server restart and can resume eligible
agent conversations; Orkestar restores metadata, marks everything interrupted,
and keeps PTY output memory-only.

- [x] Remember the split tree and focus, and return them on the next start.
      The client owns the layout, so it persists `<runtime>/layout.json` and
      rebuilds it against the terminals the daemon is still running.
- [x] Optional scrollback persistence, with the secrets caveat stated.
- [x] Automatic native session restore for adapters that reported an ID, with
      an explicit opt-out.

Progress 2026-09-16: automatic native session restore, client-side pane layout
persistence and opt-in pane history landed. When the first client connects
after a daemon restart the daemon relaunches every interrupted agent that
reported a native session ID; `ORKESTAR_AUTO_RESUME=0` disables it, and a plain
command is still never restarted. The TUI stores its split tree and focused
pane in `<runtime>/layout.json` (terminal panes only) and restores them on
start against the terminals the daemon is still running; a pane whose terminal
is gone is dropped. With `ORKESTAR_PANE_HISTORY=1` the daemon also persists
each terminal's bounded recent text and restores it into `terminal.read`, with
the secrets caveat documented.

Acceptance: restart the daemon; layout, labels and supported agent
conversations return without typing a resume command.

### M10: Declarative agents

Orkestar needs a hand-written adapter per agent, which is why Cursor and Grok
stalled. herdr ships roughly sixteen agents through detection manifests and
per-agent resume commands, so a new CLI works without code.

- [x] An agent manifest: name, executable, resume command, lifecycle detection
      rules, and supported modes.
- [x] Screen-detection fallback that infers working, blocked or idle from the
      pane when no hook channel exists. Landed 2026-09-16: a manifest may
      declare ordered detection rules, and the daemon applies them to the
      bridged terminal when the adapter has no hooks. No built-in manifest
      ships rules, so existing agents are unchanged.
- [x] Manifest registry with reload and local overrides. Landed 2026-09-16:
      manifests load at startup and a manifest may take a built-in adapter's
      name; `agent.reloadAdapters` (CLI `agent reload`) rebuilds the whole set.
- [ ] Move Cursor and Grok onto manifests with their documented resume flags
      and verify against the installed CLIs. Blocked: neither CLI is installed
      here, so their resume and hooks cannot be verified.
- [x] Hooks stay authoritative when they exist; detection is the fallback.

Progress 2026-09-16: `internal/agent/manifest` and the registry landed. A
manifest is name, executable, arguments and a resume template; the daemon
loads `<user config>/orkestar/agents/*.json` at startup (override with
`ORKESTAR_AGENT_MANIFESTS_DIR`), and a manifest may replace a built-in adapter
of the same name. A manifest agent was launched end to end. Detection rules,
reload, and the Cursor/Grok manifests remain.

Acceptance: adding an agent is a config file plus a fixture test, and Cursor
and Grok report attention and resume.

### M11: Version-tolerant protocol and live handoff

herdr negotiates client and server capabilities without matching builds, and
can hand live PTYs to a replacement server so an update does not kill work.
Orkestar refuses a version difference, and restarting it stops every process.

- [x] A capability handshake so any client and daemon within a protocol
      generation interoperate; remove the hard refusal in `--remote`. Landed
      2026-09-16: `system.ping` advertises the protocol generation, and the
      remote client refuses only a protocol difference, not a build one.
- [ ] An opt-in handoff that transfers live PTYs to a replacement daemon where
      the platform allows it. Deferred: it needs platform-level PTY transfer
      and is untestable in this environment.

Acceptance: replace the daemon binary under load and every pane keeps running.

### M12: Multi-machine federation

herdr keeps local work and several saved SSH machines in one window with a
combined agent list and independent reconnects; Orkestar has a single remote
and no saved machines.

- [x] Saved machine profiles (id, label, ssh target, remote session) with add,
      list, rename, enable, disable and remove. Landed 2026-09-16 as
      `internal/machine` plus `orkestar machine ...`, stored in
      `~/.config/orkestar/machines.json` (override with
      `ORKESTAR_MACHINES_FILE`). No credentials are stored.
- [x] Per-machine connections with independent reconnect and health checks.
      Landed 2026-09-16 in `internal/federation`: one connection per enabled
      machine, a per-machine backoff on failure, and `machine status`. A lost
      machine goes offline without moving the others.
- [x] A combined workspace and agent list with an attention rollup, and input
      routed to the selected machine. The merged board and attention rollup
      exist (`federation.Manager.Board`, `machine board`); the TUI shows every
      machine's agents inline in one sidebar with a machine column and a status
      line per machine, selects a machine (`Ctrl+b g`) and routes input to it,
      keeping each machine's own layout. A remote row is selectable and driven
      in place: interrupt, stop/clear, resume, explain and Enter-to-open go to
      that machine's daemon. Machine profiles can also be added, removed and
      enabled from the TUI (`Ctrl+b ,` then `m`).
- [ ] No local command, config or secret is copied to a remote.

Depends on M11's negotiation. Acceptance: local plus two remotes; losing one
leaves the others usable and never moves the selection.

Progress 2026-09-16: the saved-machine catalog, the federation manager
(connections, health, backoff, merged board, routing), `machine
status|board|call`, a TUI machine switcher and a merged agent sidebar landed,
tested against in-process daemons. The sidebar lists every machine's agents
with a machine column and a per-machine status line; switching with `Ctrl+b g`
detaches the current panes and restores the selected machine's own layout, and
each pane attaches through that machine's client, so input follows the
selection. Progress 2026-09-18: a remote agent row is driven in place —
interrupt, stop/clear, resume and explain call that machine's daemon, and
Enter moves to it and opens its pane — and the machine list is editable from
the TUI. ssh against a real host still cannot be verified on this machine (no
sshd).

### M13: Pane layout and daily-use parity

- [x] Swap and move panes across groups; a portable layout export and apply.
      Swap landed 2026-09-16 (`Ctrl+b p` exchanges the focused pane with the
      next, keeping split shape and ratios). Move landed 2026-09-18: `Ctrl+b m`
      then an arrow re-parents the focused pane beside its nearest neighbour
      in that direction, which changes the tree shape rather than exchanging
      contents. Portable layouts landed the same day: `Ctrl+b l` saves named
      layouts (structure plus each pane's label, command and directory),
      applies them by reusing running terminals or starting the saved command,
      and deletes them.
- [x] Richer configuration: terminal window title, sidebar row layouts and
      tokens, and themes. The window title names the focused pane, and a theme
      is chosen in `tui.json` (`"theme"`) or cycled with `t` in settings
      (palettes: `orkestar` dark default, `light`). Sidebar rows landed
      2026-09-18: `tui.json` `"sidebar"` overrides the task, agent and session
      row text with `{token}` templates, and unknown tokens are reported at
      startup.
- [ ] Inline images (already listed under M7) once a pane can hold graphics,
      since screenshots and diffs need them as much as engines do.

## v0.5.0 — 2026-09-16

M8 through M13's verifiable items shipped together as **0.5.0**: the
agent-native control surface (read/send/wait, `agent.explain`, `task.attach`,
MCP tools), session continuity (auto-resume, layout and opt-in pane history),
declarative agent manifests with detection, protocol negotiation, the
multi-machine federation catalog/manager/CLI and a TUI machine switcher with a
merged agent sidebar, pane swap, the window title and themes.

Deliberately not in 0.5.0, with reasons: Cursor and Grok resume verification
(no CLI installed), live handoff and inline images (large, and the terminal to
verify against is not here), ssh against a real host (no sshd), a single merged
sidebar that drives remote agents without switching, and M12's "no local
command or secret copied to a remote" guarantee beyond the catalog holding no
credentials.

**0.5.1** followed on 2026-09-16 with bug fixes only: four review passes over
the new surface, the suite green under `-race` and `staticcheck`. See the
changelog. The 0.5.0 caveats above are unchanged — in particular the remote
half is still verified only against in-process daemons, not ssh.

### M14: Extension surface (a decision, not a commitment)

A marketplace is out of scope in every form: no plugin index, no discovery, no
install-from-a-repository flow, and no core investment in extension
distribution.

- [x] Decided 2026-09-16: no extension surface for now. Nothing concrete wants
      to exist yet, so the core keeps growing instead. Revisit only if a real
      extension arrives that the CLI, IPC and MCP surfaces cannot express.

Deliberate non-goals, unchanged: any plugin or theme marketplace, tabs as a
separate concept, a graphics engine, and matching herdr on multiplexer surface
for its own sake. Orkestar's bet stays the workflow layer and a runtime a
person can walk away from.

## M15: Unattended operation

M0 through M13 give Orkestar work to own and a way to hand it between agents.
None of it makes walking away safe: an agent can run until a rate-limit window
is gone, a template stops when its first tasks are launched, and every
permission reply still waits on a person. The three below are ordered by what
unattended work needs first — a limit, a next step, and a policy for the
routine ask. Each builds on machinery already shipped (the watchdog, task waits
and templates, the permission inbox), so it is wiring rather than foundations.

- [x] **Usage and cost budgets per task and agent.** The watchdog added
      2026-09-18 raises attention when a turn runs long, and its own comment
      names the limit: "Orkestar cannot see token usage." It can, for agents
      that report it, and a budget is what turns that from a display into a
      stop.

      - Usage sources: Codex rollout logs already carry `token_usage_record`
        with per-turn and per-thread totals and the rate-limit window; Claude
        hook payloads and the OpenCode plugin expose what they expose, and a
        provider's own log is parsed where the hook does not. A manifest gains
        an optional usage source (a log path and a pattern) so a declarative
        agent can report without code, the same shape as M10's detection
        rules. An adapter with no source stays time-only, which is today.
      - A budget belongs to the task and is inherited by agents launched for
        it: a token ceiling and a wall-clock ceiling, each with a warn and an
        optional stop threshold. Tokens are the primary unit because that is
        what an agent reports; a dollar estimate needs a configured price
        table and is derived, never stored as the budget itself.
      - Crossing a threshold raises the same attention a crash raises.
        Crossing a stop threshold interrupts the agent, leaves the task open,
        and records why where `agent.explain`, the CLI and the task's
        artifacts can read it. A budget stops the agent, never the task:
        cancelling or completing the work stays a person's call.
      - Surfaces: usage and budget on task and agent rows, `agent.explain`,
        `orkestar task budget` / `orkestar agent usage`, and MCP read tools so
        an orchestrator can see what a pipeline is spending.
      - Acceptance: a budgeted agent that crosses its stop threshold is
        interrupted with the reason readable over IPC, CLI and MCP; a warn-only
        budget raises attention and nothing more; an adapter with no usage
        source changes nothing; the time watchdog still fires on its own.

- [x] **Auto-advance: start dependents when their dependencies finish.** A
      template starts every task with nothing blocking it at apply time and
      reports the rest as waiting, which M5 documented as "picked up with
      `task.wait` until startable" — a person or an orchestrator had to do the
      picking up. The daemon already knows the moment a task reaches done and
      already wakes waiters on it; the dependent belongs on that list.

      - A template task that names an agent gains an `auto_start` flag
        (default false, so applying a template does not silently become a
        pipeline). When a task reaching done makes such a dependent startable
        — every dependency done — the daemon launches its agent with its
        prompt. A task that already has an agent associated is never started
        again, which is what makes the rule exactly-once across a restart
        without a second bit of state to keep.
      - A cancelled dependency, or a review that will keep a task from ever
        reaching done, halts its dependents and records why; the chain stops
        because nothing can make the next link startable, and cancelling what
        is still pending stops it the rest of the way. Applying the same
        template twice is two independent sets of tasks, never a trigger for
        the first set.
      - A chain that starts agents is exactly the thing the budgets and
        permission policies above exist to make safe, which is why this sits
        after them.
      - Acceptance: a three-task chained template with `auto_start` runs from
        one apply to a finished board with no human or orchestrator call;
        cancelling the middle task leaves the third unstarted and says why;
        restarting the daemon mid-chain neither doubles nor loses a launch.

- [x] **Permission policies.** The permission inbox (M2) and the hook/plugin
      reply bridges work; every reply is still a person's. An unattended chain
      stalls on the first routine question — the same `git status`, the same
      read of a file in the workspace — and stalls with nobody watching.

      - A policy is an ordered list of rules matching the asking agent, the
        tool, and a command or path pattern, with decisions allow, deny or
        ask. First match wins, and an unmatched request keeps today's
        behavior, which is to land in the inbox. Anything a rule decides is
        recorded with the rule, the request and the time, so "why was this
        approved" is answerable afterwards, and `agent.explain` names the rule
        holding or deciding a request.
      - Written where other Orkestar configuration lives (a workspace
        `.orkestar/policy.json`, with a user-level file beneath it), validated
        whole before it is applied, the way templates are: a half-applied
        policy that silently approves is worse than a rejected one.
      - This is the inbound half of the policy and audit middleware M4 lists
        for external MCP calls. When M4 lands both should share one rule
        vocabulary rather than growing two, and it is written here so the
        second one does not invent a format the first cannot read.
      - Acceptance: a repeated ask answered by a rule with no human in the
        loop; an unmatched ask still reaches the inbox; the audit names the
        deciding rule, and a rejected policy changes nothing.

## M16: Operator surface

The person is watching more often than not, and these make that better: run the
loop without a TUI, find what happened, keep the evidence when it scrolls away,
and make a notification something you can act on. Ordered by how much daily
friction each removes. `orkestar run` wants M15's budgets before it is safe in
CI, which is the one dependency here.

- [x] **Headless `orkestar run`.** The control surface is all there — start,
      send, read, wait, attach, review — over IPC, CLI and MCP, but running one
      piece of work start to finish still means composing several verbs in a
      shell script, each with its own JSON handling. One command should do it:
      given a workspace or a new directory, a task or a template, and an agent,
      it creates what is missing, starts the work, waits on `task.wait`, prints
      a machine-readable summary (status, artifacts, diff, usage once M15 has
      it), and exits non-zero when the work failed or was cancelled.

      - Interactive input is out of scope by definition: with no TTY the run
        either finishes, hits a permission that policy answers, or reports the
        stall. `--timeout` bounds it the way every other wait is bounded, and
        an interrupt stops the run cleanly without killing the daemon or the
        other sessions on it.
      - This is the entry point CI and the M4/M7 pipelines need, and it is the
        cheapest way to find out whether an Orkestar workflow is reproducible
        without a person at the terminal.
      - Acceptance: on a machine with no daemon running, one command creates a
        workspace, creates or applies work, runs it to done, and prints a
        parseable summary whose exit code matches the outcome; interrupting
        mid-run leaves the daemon and its other sessions running.

- [x] **Global search across panes, tasks and artifacts.** What a pane printed
      is daemon-owned and bounded, tasks and artifacts are in the store, and
      pane labels are client-side; nothing searches across them. "Where did
      that error appear" today means visiting panes one at a time.

      - One query over the visible screen and bounded scrollback of every
        terminal (the content `terminal.read` already serves), pane labels,
        task titles and descriptions, artifacts and templates. A TUI overlay
        jumps to the pane or task that matched and shows the surrounding
        lines; `orkestar search` and an MCP tool give a script and an agent the
        same thing, which is how one agent finds where another failed.
      - It starts as a scan over the bounded history rather than a full-text
        index: the history is already capped and local, and an index is a
        store change whose cost is not justified until a scan is felt to be
        slow. Results are ordered deterministically (kind, then recency, then
        identity) so two runs of the same query agree.
      - Acceptance: a phrase printed in any pane is found and selecting the
        result focuses that pane; task titles and artifact labels match; a
        terminal with no output and a workspace with nothing running both
        answer with an empty result rather than an error.

- [x] **Recording a pane as a review artifact.** The artifact kinds cover a
      diff, a test result, a log, a screenshot, a build and a review; the
      timeline of a run is none of them, and a session's scrollback is bounded
      and dropped when its terminal ends. When an agent's behavior is the
      thing under review, the diff alone does not show it.

      - Recording captures a terminal's output chunks with timestamps into a
        file the daemon owns, bounded by size and age the way pane history is,
        opt-in per terminal, and registered on its task as an artifact (a
        `recording` kind, or `log` with a declared format). The format is
        asciicast-compatible so the file is useful outside Orkestar, and the
        review pane can replay it beside the diff.
      - Secrets: a recording is pane history written to disk and carries the
        same caveat, so it is off by default, announced while active, and
        removed with its terminal or task. A template never starts one
        silently.
      - Acceptance: `orkestar terminal record start|stop` produces a file that
        replays in the TUI review pane and parses as asciicast; an unrecorded
        terminal writes nothing; a recording past its bound is trimmed rather
        than growing without limit.

- [x] **Notifications that act.** The bell and the desktop notification fire,
      and `notify.go`'s own comment admits the second gap: "This still needs a
      client running." Either way a notification says what happened and offers
      nothing to do about it; acting means finding the pane and reaching for
      the right binding.

      - Where the platform can carry an action (Linux `notify-send --action`;
        macOS through `terminal-notifier` when it is installed, since
        AppleScript notifications have no buttons; Windows has no equivalent),
        the notification offers the one action that applies — focus the pane,
        or approve or deny the waiting permission. The action posts an IPC call
        to the daemon, so it works without the TUI holding focus.
      - Where the platform cannot, the notification names the pane and the
        binding instead of pretending, and the settings screen says which
        behavior this machine gets, the way it already reports whether
        notifications are supported at all.
      - Delivery moves behind the daemon so a change worth notifying happens
        with no client attached, which is the caveat the current code writes
        down. Cross-machine delivery stays out of scope: a notification is a
        local desktop thing, and reaching a phone belongs with remote access.
      - Acceptance: on a platform with actions, approving from the
        notification replies to the waiting hook and the row clears with no TUI
        focus; on a platform without, the capability is reported and no action
        is offered; a bell-worthy change with no client attached still posts
        once, not once per client that later connects.

## M17: GitHub issue and PR sync

The first integration with a service rather than an agent runtime or an engine,
and the largest guess about how somebody else works, which is why it is last and
alone. The rule it
sets is the one every later integration should follow: the platform's own CLI
is the authentication boundary, and Orkestar stores no credential.

- [x] **Import an issue as a task, open a PR from a reviewed one.** Work
      usually starts as an issue and ends as a pull request, and both are
      copied by hand into Orkestar today.

      - Import (`orkestar task import --github <url|number>`) shells out to
        `gh`, which owns authentication, and creates a task whose description
        and link come from the issue. Orkestar never reads or stores a token,
        consistent with the machine catalog and M12's no-secret rule.
      - Export (`orkestar task pr <id>`) opens a PR from the task's worktree
        branch once the review gate passes, with the title and body taken from
        the task, the reviewer's verdict linked, and the PR URL recorded as an
        artifact. It never merges, closes an issue or force-pushes; those stay
        the platform's and the person's.
      - Sync starts one-way (issue to task, PR link back), because between two
        sources of truth the hard part is not the API but deciding which side
        wins. A missing or unauthenticated `gh` produces a clear error and no
        partial task, the rule templates already follow.
      - Acceptance: with `gh` authenticated, importing an issue creates a
        linked task and opening a PR from a reviewed task succeeds with the URL
        on the task; without `gh`, both fail before creating anything.

## M15–M17 progress — 2026-09-21

Specified and implemented the same day from the tree at `b853b98`. What each
one actually is, and what is still missing:

- **Budgets** live on the task (`token_budget`, `time_budget_seconds`,
  `budget_action`) and are checked on every hook and on the watchdog tick.
  Usage comes from the Claude transcript, the Codex rollout found by native
  session ID, and the OpenCode plugin's token totals; the daemon reads each
  incrementally from a stored offset. Crossing a budget raises attention and
  records a `log` artifact on the task; `stop` also interrupts the agent.
  `agent.explain` reports the budget and spend. **Not implemented**: a
  manifest-declared usage source, so a declarative agent still reports no
  tokens, and dollar estimates, which need a price table.
- **Auto-advance** is `auto_start` on a template task, recorded as
  `task.setAutoStart` and launched by a daemon board watcher once every
  dependency is done. The assignment is the exactly-once marker, so a restart
  neither doubles nor loses a launch; a failed launch is recorded on the task
  and not retried. `AppliedTemplate.AutoStarting` reports what the daemon now
  owns.
- **Permission policies** are `.orkestar/policy.json` with a user-level file
  beneath it, first match wins, and a rule that would match everything with
  allow or deny is refused. Decisions are audited and persisted
  (`policy.audit`), a hypothetical request can be checked (`policy.check`), and
  an ask that reaches the inbox names the rule consulted. **Not implemented**:
  sharing M4's rule vocabulary, since M4 has not started.
- **`orkestar run`** creates or reuses a workspace, creates a task or applies a
  template, launches an agent, waits on `task.wait`, and prints a JSON summary;
  a cancelled or timed-out run exits non-zero after printing. A template with
  hand-start waiting tasks is refused rather than left to time out.
- **Search** scans terminal screens and bounded scrollback, tasks, artifacts
  and templates over IPC, CLI and MCP, with pane-label matches added
  client-side. It is a scan, not an index.
- **Recording** captures a terminal as an asciicast v2 file, capped at 8 MiB,
  and attaches it to a task as a `recording` artifact. The TUI has a recordings
  overlay (`Ctrl+b R`) and a replay pane. The secrets caveat is the pane-history
  one: a recording is terminal text written to disk.
- **Notifications** are actionable where the platform reports the choice
  (Linux `notify-send --wait --action`); macOS AppleScript notifications have
  no buttons, so they name the change and nothing more. The daemon posts only
  when no client is attached; the interface keeps focus-aware suppression.
  Cross-machine delivery remains out of scope.
- **GitHub** import and PR use the `gh` CLI as the authentication boundary;
  no token is stored. `task import` creates a task from an issue, `task pr`
  opens a pull request from a done task's branch and records the URL as a
  `pull_request` artifact. Sync is one-way. Verified against a fake `gh` on
  PATH, not a real GitHub account.

M4 still blocks M7, and nothing here changes that.

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
