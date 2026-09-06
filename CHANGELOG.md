# Changelog

## 0.4.0

Two milestones: agents can now coordinate rather than only run, and somebody
other than the author can install and drive the thing. An agent waits on
another agent's work instead of polling for it, a pipeline is declared once in
a file, and a daemon on another machine is reachable over ssh. Keys rebind, a
right-click says what a pane can do, and a curl one-liner installs it.

- Right-click a pane for a menu of what it can do, listing only what applies to
  that pane and showing the key for each.
- Run separate daemons with `orkestar --session <name>`, each with its own
  socket, database and log. The default session is unchanged.
- Rename the focused pane with `Ctrl+b r`, so a layout of shells is not a row
  of identical boxes.
- Launch Cursor and Grok in a pane. Both claim only what has been checked:
  interactive with prompt and interrupt, no managed mode and no resume, and no
  lifecycle beyond started, stopped and crashed, since neither CLI's hook
  contract has been verified.
- Install on macOS and Linux with a curl one-liner, or through mise's `ubi`
  backend. The installer verifies the release checksum before unpacking, and
  says what to add to `PATH` rather than editing a shell profile.
- Rebind any key with a `keys` map in `tui.json`, including the prefix.
  Dispatch and the help line both read from it, so a rebinding cannot leave the
  help describing a keyboard that no longer exists. Unknown actions and keys
  bound twice are reported on startup.
- Attach to a daemon on another machine with `orkestar --remote user@host`,
  which runs the interface locally over an ssh session to
  `orkestar daemon proxy`. Authentication is ssh's; the daemon still listens
  only on its owner-only local socket.
- Reuse IPC connections across calls rather than dialling one per request, and
  bound how often a screen frame is offered to a subscriber. A frame is the
  whole screen, which costs nothing locally and a great deal over a link.
- Name the task a pane is working on its border and in the header, mark the
  tasks something is showing in the Tasks list, and make `Enter` on a task
  focus, open or cycle its panes.
- Declare a pipeline once in `.orkestar/templates/<name>.json` and apply it:
  its tasks, their dependencies, their worktrees, and the agents that work
  them. `orkestar template list|apply`, or `template_list`/`template_apply`
  over MCP. With starting enabled, only the tasks nothing is blocking are
  launched; the rest are reported as waiting.
- Reuse the workspace already rooted at a directory instead of creating
  another. The MCP tool has always described itself as "create or reuse", and
  an agent that calls it before every task was scattering work across
  duplicates.
- Wait for work instead of polling it: `task.wait` and `agent.wait` over IPC,
  `task_wait` and `agent_wait` over MCP, and `orkestar task wait`. A task can be
  waited on until done, finished either way, or startable; an agent until it is
  blocked, idle or stopped. A wait that can no longer be satisfied fails rather
  than holding its connection to the deadline, and every wait is bounded.
- Tell a connecting MCP client how the tools fit together, as server
  instructions rather than a page nobody reads: create a task, give it a
  worktree, start an agent on it, wait, review, complete. Also printable with
  `orkestar mcp instructions`.
- Post a task finishing, or an agent stopping with its work unfinished, as a
  desktop notification as well as a bell, and only while the terminal is not
  focused. `n` in settings, or `"notifications": false`. macOS and Linux only.
- Order tasks and artifacts by when they were created rather than only by their
  timestamps. Two created in the same clock tick compared equal, so the sidebar
  could show them in a different order on each refresh.

## 0.3.0

Tasks stop being a list beside the agents and start describing them: an agent
is launched for a task, told what to do, and the board follows what it does.
Two prompts that never arrived and one interrupt reported as a crash are
fixed along the way.

- Edit a task after creating it: `e` on a task in the sidebar, `task_update`
  over MCP, or `orkestar task edit` with `--title`, `--description` and
  `--depends-on`. Only the fields given change. A dependency added later is
  rejected if it would close a cycle, which `task create` could never build.
- Collect a description when creating a task. `Tab` moves between the title and
  description; `Ctrl+R` toggles auto-review, which `Tab` used to do.
- Let an agent hand work to another agent over MCP: `task_start` launches an
  agent for a task and tells it what to do, `agent_prompt` follows up, and
  `agent_list` reports running sessions and the adapters available. There is
  deliberately no way to stop an agent over MCP.
- Carry an opening prompt through `agent.launch` and deliver it once the
  session reports it has started, so a caller need not guess how long an
  interactive CLI takes to come up.
- End a prompt with a carriage return rather than a line feed. Agent CLIs read
  the terminal raw and act on carriage return, so `agent.prompt` had never
  submitted anything to an interactive session: the text arrived and sat in the
  input box.
- Launch an agent for a task, from the Tasks list with `a`, with `task_id` on
  `agent.launch`, or with
  `orkestar agent launch <workspace> <adapter> --task=<id>`. The session starts
  in the task's worktree and the assignment is recorded both ways in one call.
- Move a task from pending to in_progress on its agent's first prompt or tool
  use, so the board follows the work instead of waiting to be told. A task that
  is already done, cancelled or blocked is left alone.
- Report an agent that exits on an interrupt as stopped rather than crashed,
  whether the interrupt came from the sidebar or was typed into its pane.
  Anything else it dies of is still a crash.
- Ring the terminal bell when a task finishes, or when an agent stops with its
  task still open. Turn it off with `b` in settings or `"bell": false`.

## 0.2.0

Terminal correctness and throughput: a leaked window title no longer lands in an
agent's prompt, sustained output costs a fraction of what it did, and a terminal
pane's text can be selected and copied.

- Stop a UTF-8 character inside an OSC, DCS, SOS, PM or APC sequence from
  ending it early. Claude Code sets the window title to "✳ <conversation>"
  when a turn finishes, and the 9C byte of U+2733 was read as a String
  Terminator, printing the rest of the title into the agent pane's input box.
- Select text in a terminal pane by dragging, and copy it to the system
  clipboard on release. Hold Shift to select past a program that has taken the
  mouse over.
- Render a screen when a client is ready for it rather than for every chunk of
  PTY output, and hold client repaints to one per 16ms. Sustained output cost
  the daemon 136us per chunk and now costs 18us.

## 0.1.0

Initial versioned release: daemon-owned agent and shell sessions, reconnectable
terminal panes, workspaces and tasks, code review and editing, and SQLite metadata.

- Preserve Shift+Enter as a modified Enter key in embedded terminals.
- Make confirmed reset stop the daemon and clear state in one command, including
  upgrades from daemons without the reset method.
- Add version reporting, macOS/Linux archives, a Homebrew formula, and Windows
  ConPTY/named-pipe support with PowerShell build and installation scripts.
