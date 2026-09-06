# Changelog

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
