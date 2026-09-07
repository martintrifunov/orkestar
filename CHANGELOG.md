# Changelog

## 0.4.2

A crash in one terminal pane's rendering used to take the whole daemon down
with it, along with every task and session it owned. Panics are now caught
per pane and per goroutine instead of one bug ending everything, and closing
an agent session actually cancels whatever prompt it had in flight instead of
leaving it running unsupervised.

- Recover from a panic in a terminal pane's rendering, in agent lifecycle
  watching, in opening-prompt delivery, and in the IPC accept and
  per-connection loops, instead of one panic crashing the daemon and every
  session and task it owned.
- Cancel an in-flight OpenCode prompt when its session closes, and publish a
  session's stopped event before its PTY channel closes, so nothing keeps
  running unsupervised after a session ends.
- Reset the task board in place instead of swapping it, so a caller blocked
  in `task.wait` is woken instead of left listening to an object nobody will
  ever mutate again.
- Close a race that could create two workspaces for one directory.
- Fail closed instead of silently approving a task when automatic review has
  no reviewer configured, no workspace, or a diff too large to review. A task
  with no worktree still has nothing to review, so it is approved outright.
- Fix a wait timeout overflow that could let a very large requested duration
  through uncapped.
- Report a template's tasks and worktrees created so far instead of
  discarding them when a later one fails, and give a template-launched agent
  a default prompt when it declares none.
- Keep a resumed agent tied to its task.
- Parse `git status` by NUL so a renamed, copied, or oddly named file no
  longer breaks automatic review, and include untracked files in the diff a
  reviewer sees.
- Make an IPC call retry-safe against a partial write, release its connection
  on cancellation, and give the ssh transport real deadlines and a race-free
  shutdown.
- Show gitignored files and empty directories in the file viewer, and browse
  into and out of directories in both file pickers instead of only searching
  a flat list.
- Turn off local file browsing, editing and review over a remote session,
  which read and wrote the wrong machine over ssh.
- Send pasted text to whichever prompt is focused, launch a task-picked agent
  into the task's own workspace, and follow the configured next-pane key
  instead of a hardcoded one.
- Fix the editor's size limit to account for the selection being replaced,
  and keep a mouse-drag selection extending past a pane's own content instead
  of freezing at the edge.

## 0.4.1

Fixes for eight things a review of 0.4.0 found, three of which meant a headline
feature did not work at all.

- Start a named session's daemon in that session's directory. `--session` was
  passed to the parent and not the child, so auto-starting one bound the
  default socket and opened the default database while the caller waited for a
  socket that never appeared.
- Keep an ssh session alive past the call that opened it. It was tied to the
  dial's context, which every caller cancels on return, so a remote pane died
  before its first frame and no connection was ever reused.
- Give a remote interface a directory to work in, taken from an existing
  workspace or given as `orkestar --remote <host> <directory>`. Without one,
  creating a shell, an agent or a task failed on an empty path.
- Wire the right-click menu's "Take control" to something. It was offered and
  did nothing.
- Send the bound prefix through to a pane when it is pressed twice, rather than
  always `Ctrl+B`. Rebinding the prefix left no way to reach a nested tmux.
- Honour a rebound `edit-file` from the sidebar, which still looked for `e`.
- Report a template whose tasks were created but whose agent could not start,
  in the result rather than as an error that discarded it.
- Treat `orkestar task wait --timeout=0` as the default wait rather than a
  15-second client deadline against a 5-minute server one.

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
