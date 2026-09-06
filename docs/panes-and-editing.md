# Panes and editing

Press and release **Ctrl+b**, then press the action key. Each action needs its
own prefix. The footer changes while a prefix is pending. In sidebar focus,
`v`, `s` and `o` also work directly. Typing these letters in an agent terminal
continues to type into that agent.

| Action | Shortcut |
| --- | --- |
| Split: open a new shell beside the focused pane | Ctrl+b, v |
| Split: open a new shell below the focused pane | Ctrl+b, s |
| Focus the next open pane | F6 or Ctrl+b, o |
| Zoom the focused pane, and back | Ctrl+b, z |
| Move the enclosing split's divider | Ctrl+b, arrow keys |
| Open another shell | Ctrl+b, n |
| Open agent picker | Ctrl+b, a |
| Open workspace/task worktree review | Ctrl+b, d |
| Find/open/create a file | Ctrl+b, e |
| Editor settings, including syntax highlighting | Ctrl+b, comma |
| Show or hide the file viewer | Ctrl+b, f |
| Scrollback for the focused terminal | Ctrl+b, [ |
| Focus sidebar | Ctrl+b, Tab |
| Close focused pane | Ctrl+b, q |
| Explicitly discard a dirty standard editor | Ctrl+b, x |

## Sessions and agents

`X` in the Sessions or Agents section stops whatever is selected, or clears it
from the list once it has finished. Stopping something that is still running
asks for a second `X` first: the daemon holds that work independently of the UI,
so ending it should be deliberate. Clearing a finished entry happens at once, and
also closes any pane still attached to it.

`i` interrupts an agent's current turn without ending the session, the same
thing Ctrl+C does when typed into its terminal.

A session that belongs to an agent cannot be removed on its own; remove the
agent and its terminal goes too. Sessions restored after a daemon restart are
marked interrupted and can be cleared the same way. The command line has the
same operations: `orkestar terminal stop|remove` and
`orkestar agent stop|remove|interrupt`.

## File viewer

`Ctrl+b f` opens a file tree on the right edge of the window, the same width and
height as the sidebar on the left. It is collapsed by default and reads nothing
until you open it, so it has no cost while hidden.

While it is open it follows the workspace, re-reading every couple of seconds so
files an agent writes or removes appear on their own. Expanded directories and
the selected entry are tracked by path, so a refresh never collapses the tree or
moves your selection. Ignored files are excluded through Git, with a bounded
directory walk outside a repository.

Only one of the sidebar, the panes and the viewer holds the keyboard at a time,
and a view that covers the whole content area, such as scrollback, takes it from
all three until it closes.

Arrow keys or `k`/`j` move. `Enter` or `→` expands a directory, or opens a file
in an editor pane below the focused pane, where it gets the full width rather
than sharing it with the tree. `←` collapses a directory, or steps out to its parent. `r`
re-reads immediately, `Esc` hands focus back to the panes, and `Tab` goes to the
sidebar. Clicking an entry selects it and opens or expands it, and the wheel
scrolls the tree.

The viewer shows the workspace Orkestar was started in. In a window too narrow to
keep a usable pane beside it, it stays hidden rather than squeezing the panes.

## Handing a task to an agent

Pressing `a` with a task selected launches an agent **for that task**. The
daemon starts the session in the task's worktree when it has one, so the
agent's changes land on the task's branch and `d` shows them, and it records
the agent on the task and the task on the agent in the same call, so the two
can never disagree about who owns the work. Pressing `a` anywhere else still
launches a free-standing session.

From then on the board follows the session. The first prompt or tool use moves
a **pending** task to **in_progress**; a task that is already in progress, done
or cancelled is left alone, because a prompt says work is happening, not that
someone was wrong to have decided otherwise. A task blocked on an unfinished
dependency stays pending, and the agent is never stalled over it.

The Tasks list names the agent working each task and its state, the Agents list
names the task each session is on, and a task nobody has started says so.

The same hand-off from the command line:

```
orkestar agent launch <workspace-id> <adapter> --task=<task-id>
```

An agent can do the same through MCP, which is what makes one agent able to
hand work to another: `agent_list` reports the running sessions and the
adapters available to launch, `task_start` launches one for a task and tells it
what to do, and `agent_prompt` follows up. The task already knows its
workspace, so `task_start` takes only a task and, when more than one adapter is
registered, which to use.

One call is the whole hand-off. `task_start` carries a prompt — the task's own
title and description unless you pass one, or an empty string for a bare
session — and the daemon holds it until the agent's session reports it has
started, so the caller does not have to know how long a CLI takes to come up.

That waiting is not politeness. An interactive agent owns its terminal from the
moment it is spawned, but its interface is not reading keys yet: text written
that early is buffered and shows up in the input box, while the Enter after it
is discarded, leaving the prompt sitting there submitted by nobody. The daemon
waits for the session's first hook, which is the agent's own runtime calling
back, then settles briefly before typing. Without hooks the prompt still goes,
late rather than never.

Deliberately absent: MCP has no way to **stop** an agent. Starting work is
recoverable — a session that turns out to be wrong can be closed from the
sidebar — while an agent ending another agent's session destroys work in
progress with nobody watching. Stopping stays a human action, from the TUI or
the CLI.

## Editing a task

`e` on a selected task reopens the prompt over it, filled in with what is
already there. `Tab` moves between the title and the description; `Enter`
saves. The same prompt creates tasks with `c`, where `Ctrl+R` toggles
auto-review — `Tab` no longer does, because it now moves between fields.

Auto-review is missing from the edit prompt on purpose. It describes the gate
the task was created under, and offering it here would make it easy to drop a
review someone deliberately asked for.

From the command line, only the flags you pass change:

```
orkestar task edit <task-id> [--title=…] [--description=…] [--depends-on=id1,id2]
```

`--depends-on` replaces the whole list, and an empty value clears it.

### Dependencies added later

`task create` cannot build a dependency cycle: a task may only depend on tasks
that already exist, so its edges always point backwards in time. An edit has no
such guarantee, and a cycle would leave every task in it permanently
unstartable, each waiting on the next. Orkestar rejects one and names the path
it would have closed, rather than storing a board that can never move.

## Named sessions

One daemon per machine assumes one piece of work at a time. Two projects that
should not share a board — different repositories, different agents, different
tasks — have no way to be told apart otherwise.

```
orkestar --session api
orkestar --session api task list
orkestar --session api daemon stop
```

A named session is a whole separate daemon: its own socket, database and log,
under `sessions/<name>` in the runtime directory. The default session keeps the
path it always had, so an existing daemon and everything it remembers stay
exactly where they were.

## Renaming a pane

`Ctrl+b r` renames the focused pane. Every pane is otherwise named after the
command that started it, so a layout of shells reads as a row of identical
boxes. An empty name restores the default, since a pane with no name at all is
harder to place than one named after its command.

The name is the client's own. The daemon owns the process and has no opinion
about what a pane is called, which is also why a rename does not follow the
session to another client.

## Key bindings

Every binding can be changed. People arrive from tmux, zellij and vim with
fingers that expect something else, and a tool that cannot be rebound is one
they have to think about while using it.

```json
{
  "keys": {
    "prefix": "ctrl+a",
    "new-task": "N",
    "next-pane": "ctrl+n"
  }
}
```

Anything absent keeps its default, so only what your fingers already expect
needs writing down. An action that does not exist, or a key bound to two
actions, is reported in the notice line when the interface starts rather than
silently doing nothing.

The help line along the bottom is built from the same map, so it describes the
keyboard you actually have.

A few keys are deliberately fixed: the arrows and their vim equivalents for
moving a selection, `esc`, and the editor's own `Ctrl+S`/`Ctrl+Z` and friends,
which follow conventions from outside Orkestar.

Some actions live behind the prefix and some do not, and a few do both. `n`
opens a shell from the sidebar and so does the prefix; `e` edits a task in the
sidebar while the prefix opens a file; `q` quits from the sidebar while the
prefix closes a pane. Rebinding one does not disturb the other, because they
are separate actions rather than the same letter twice.

## Attaching to a daemon on another machine

`orkestar --remote user@host` runs the interface here and the daemon there.
That is the point of it: notifications, the clipboard and the terminal belong
to the machine you are sitting at, while the agents keep running on the one
with the work. Sitting in an ssh session and running `orkestar` has always
worked and still does; what it cannot do is tell your laptop that a task
finished.

**The transport is ssh and so is the authentication.** Each connection is
`ssh <host> orkestar daemon proxy`, whose stdio is the far end's socket. Your
keys, agent, `~/.ssh/config`, jump hosts and second factors all apply, and
Orkestar has no credential of its own to get wrong. The daemon keeps its
owner-only local socket and never listens on a network — there is nothing to
expose, and nothing to configure. A daemon that is not running on the far side
is started, exactly as it is locally.

Two things make it affordable. The client reuses connections, so the
once-a-second snapshot does not open an ssh session each time, and ssh's own
connection sharing means the sessions that are opened skip the handshake.

A version mismatch is refused up front, naming both sides. Locally the client
and daemon are the same binary; across machines they are two installs, and
two builds agreeing on the protocol by accident is not something to find out
halfway through a session.

## Finding the work on screen

Panes already belong to tasks — an agent is launched for one and its terminal
is the pane — but nothing said so. Three agent panes running the same CLI read
identically, and getting from a task on the board to the pane doing it meant
guessing.

A pane's border now names the task it is working, and so does the header for
the focused one. The Tasks list marks every task something is showing, with a
count when more than one pane is on it.

`Enter` on a task shows that work: it focuses the pane if one is open, opens it
if the agent is running but its pane was closed, and cycles within the task
when several panes are on it — the one place a task's panes act as a group. A
task nobody has started says so instead.

This is the grouping other multiplexers get from arbitrary tabs, taken from the
structure Orkestar already models rather than a second one laid beside it.

## Workflow templates

A piece of work that happens the same way every time is declared once, in
`.orkestar/templates/<name>.json` inside the workspace, so it is committed
beside the code it describes. A pipeline that only exists in someone's shell
history is not one the next person can run.

```json
{
  "name": "release",
  "tasks": [
    {"key": "tests", "title": "Run the suite", "worktree": true, "agent": "claude-code"},
    {"key": "notes", "title": "Write the release notes", "depends_on": ["tests"]}
  ]
}
```

`key` names a task for the others to depend on and never leaves the file:
applying resolves the keys to real task IDs. Tasks are created in dependency
order, since a task can only depend on tasks that already exist, and a template
whose tasks depend on each other in a loop is refused before anything is
created rather than failing halfway through.

```
orkestar template list <workspace-id>
orkestar template apply <workspace-id> <name> [--start]
```

With `--start`, the agents the template names are launched — but only on the
tasks nothing is blocking. Starting the rest would mean agents sitting idle
against work they cannot begin, so they come back under `waiting`, and
`task wait <id> startable` is how they are picked up. Over MCP these are
`template_list` and `template_apply`.

## Waiting on work

Every other method answers immediately, which is fine for a person watching a
sidebar and useless for an agent: following another agent's work by asking
again and again costs a model turn per ask. `task.wait` and `agent.wait` block
until something is true instead.

```
orkestar task wait <task-id> [done|finished|startable] [--timeout=300]
```

A task can be waited on until it is `done`, `finished` either way, or
`startable` — nothing blocks it any more, which is what to use when holding a
dependency. An agent can be waited on until it is `blocked` and genuinely
cannot continue without someone, `idle` and wanting input, or `stopped`.

A wait that can no longer be satisfied fails rather than sitting until its
deadline: waiting for a cancelled task to be done, or a stopped agent to go
idle, is a caller's mistake and it should hear about it. Every wait is bounded,
default five minutes and at most an hour, because a wait holds its connection
open by design.

Over MCP these are `task_wait` and `agent_wait`, which is what turns "an agent
can start work" into "an agent can run a pipeline".

An agent is told about them when it connects: the MCP server sends instructions
describing the whole loop, because tool descriptions cover one call each and
never the shape of the work, and an agent that has not been told waiting exists
will poll instead. `orkestar mcp instructions` prints the same text for an
agent that drives Orkestar through the CLI.

## The bell

Orkestar rings the terminal bell twice: when a task reaches **done**, and when
an agent stops, crashes or is interrupted while the task it was working is
still open. The second is the one that earns it — a session that stops on its
own announces nothing, it simply stops producing output.

Nothing else rings. Starting a task, cancelling one, and an agent stopping
after its task was finished or cancelled are all silent, and the snapshot the
TUI loads on attach never rings, or every task finished yesterday would.

What the bell does — a sound, a flash, a notification badge — is the
terminal's business. Turn it off with `b` in settings, or `"bell": false` in
`tui.json`.

Those same two moments are also posted as a desktop notification, but only
while the terminal does **not** have focus. The bell is for someone sitting in
front of it and says "glance up"; a notification is for someone in another
window, and firing one every time while they are reading the pane it is about
is just noise. A terminal that does not report focus is treated as focused, so
the worst case is a missing notification rather than a stream of them.

Turn them off with `n` in settings, or `"notifications": false`. macOS posts
through `osascript` and Linux through `notify-send`, if it is installed;
Windows has none, because a toast needs a registered application identity or a
PowerShell module that is not there by default, and the settings screen says so
rather than offering a switch that does nothing.

This still needs a client running. Detaching entirely leaves nothing to notice
the change, and closing that gap means the daemon reaching a device rather than
a desktop, which belongs with remote access.

## Selecting and copying in a terminal pane

Dragging with the left button over a terminal pane selects its visible screen
and copies the selection to the system clipboard on release, reporting how many
lines it took. The selection flows from the first cell to the last through the
ends of the rows between them, the way a terminal selects, and it covers the
screen rather than the scrollback, so `Ctrl+b [` is still how you reach earlier
output.

A click that does not move only focuses the pane; it leaves the clipboard
alone. Typing into the pane, resizing it, and changing the layout all drop the
highlight, because each of them puts different text where it was drawn.

A program that has asked for mouse reporting receives the drag itself. Hold
Shift to select past it, which is the same gesture terminals have always used
for this.

## Scrolling and scrollback

The mouse wheel scrolls whichever review or editor pane is under the pointer,
and scrolls the scrollback view once it is open. It does nothing over a terminal
pane, unless that program has asked for mouse reporting, in which case the
program receives the event itself.

Scrollback is `Ctrl+b [`. It replaces the content area with the focused
terminal's last 2,000 lines, so it is a deliberate action rather than something
a stray wheel movement can trigger. Page Up, Page Down, the arrow keys and the
wheel move through it, and `Esc` returns to the panes.

## Splits

Splits nest. `Ctrl+b s` then `Ctrl+b v` gives three panes: the first pane on
top, and the second and third side by side beneath it. Each split divides the
focused pane in half and leaves every other pane where it was. New agents,
review panes and editors opened without a split key divide the focused pane
along its longer edge. Closing a pane hands its space to the pane it was split
from. The new pane always lands beside the pane that was focused when you
pressed the key, even if you focus something else while the shell starts.

Panes are labelled in their top border with the document they hold or the
command they run, so a screen full of shells stays readable.

Splits start even and can be moved. `Ctrl+b` then an arrow moves the divider of
the nearest enclosing split in that direction, by about a sixteenth of the space
being divided. The arrows repeat: the prefix stays armed after a resize, so the
divider can be walked to where you want it without pressing `Ctrl+b` again.
`Esc`, or any other action, ends the repeat. Dragging a divider with the mouse
does the same thing. Both stop before either side becomes too small to use.

`Ctrl+b z` zooms the focused pane to fill the whole area and back again. The
other panes keep their processes and their place in the layout, and `F6` still
cycles through them while zoomed, swapping which one fills the space.

The default limit is 16 open panes; set `"max_panes"` in `tui.json` (1 to 64).
Above the limit, opening or splitting is refused with a notice. Nothing is
replaced silently. When the window is too small for every split, only the
focused pane is shown with a hint to enlarge the terminal; hidden panes stay
attached and F6 or `Ctrl+b o` still cycles through them. Click any pane to
focus it.

## Tasks

The Tasks section of the sidebar is a board, not just a list. Select it with
`Tab`, then:

| Action | Shortcut |
| --- | --- |
| Create a task | c |
| Its diff and the latest reviewer verdict | d |
| Mark it done | m |
| Cancel it | x |
| Create or remove its Git worktree | w |
| Assign it to the highlighted agent | t |

The create prompt takes a title and one choice. Automatic review is on by
default, matching the command line: the daemon runs a reviewer agent when you
complete the task and refuses to finish it unless the reviewer approves. `Tab`
turns that off for the task you are creating. Completing a reviewed task can
take a while, and the header says a reviewer is running while it does.

`w` gives the task its own worktree and branch beside your checkout, which is
where an agent should make its changes. The task diff (`d`) reads from that
worktree, so it reports the missing step rather than an error if there is not
one yet. `t` assigns the task to whichever agent is highlighted in the Agents
section, which stays visible while you choose.

The selected task shows one extra line with whatever still applies to it: how
many unfinished dependencies block it, whether completing it needs a review,
which agent owns it, and whether it has a worktree. Dependencies come from
`orkestar task create --depends-on`.

## Review

The review pane lists changed files, colored additions/deletions and old/new
line numbers. It includes untracked files as additions. Use `[` / `]` to select
a file, or click its name. Scroll with the wheel, arrows or Page Up/Down. `r`
refreshes after an agent or editor saves. `e` or Enter opens the selected file
using your configured editor. Changes belong to the workspace/worktree; the view
does not distinguish edits made by you from edits made by an agent.

## Standard editor

Ctrl+b, e opens a searchable file picker. Type a path or part of a filename,
select with arrows and press Enter. If no file matches, Enter creates that path
on the first save (its parent directory must exist). Ctrl+P opens the picker
again from the standard editor.

- Ctrl+S saves; Ctrl+Z undoes; Ctrl+Y or Ctrl+Shift+Z redoes.
- Ctrl+A selects all; Shift+arrows select; Ctrl+arrows move by word.
- Mouse click positions the cursor; drag or Shift+click selects.
- Ctrl+C/X copies/cuts; Ctrl+V requests paste from the terminal clipboard.
  Your terminal's ordinary paste shortcut also works.
- Ctrl+F searches; Enter finds the next match; Esc closes search.
- Home/End, Page Up/Down and the wheel navigate the file.

## Syntax highlighting

Source and configuration files are colored as you edit them. The language is
picked from the filename, or from the content when there is no useful extension,
so a shebang script called `deploy` is still recognized. Around 300 languages are
covered, including Go, Python, TypeScript, JavaScript, Rust, C, C++, Java, Ruby,
PHP, SQL, HTML, CSS, YAML, TOML, JSON, Bash, Dockerfile, Makefile, Terraform and
Markdown. The status line names the detected language next to the cursor
position, which is the quickest way to confirm highlighting is working.

The colors are Orkestar's own: the accent gold for keywords, and the same green,
salmon and blue the review pane uses, plus teal for types, peach for numbers,
violet for builtins and constants, and slate for operators. Comments use the
interface gray. Plain identifiers keep the default foreground so code does not
turn into a wall of color. Selected text always wins over syntax color.

Press `h` in the editor settings (`Ctrl+b`, comma) to turn highlighting off or
on, including for files that are already open, or set `"syntax": false` in
`tui.json`. Files above 256 KiB are shown uncolored. Colouring runs in the
background, so typing never waits for it.

If an agent changes the file after it was opened, saving reports a conflict and
keeps the agent's disk version. Keep or copy your buffer, discard/reopen, and
reconcile the changes. Normal pane closing and quitting refuse unsaved standard
buffers. Unsaved buffers do not survive forcibly closing the host terminal.

## Vim, Nano and custom editors

Ctrl+b, comma opens settings. Choose `1` standard, `2` Vim or `3` Nano. The
selection is saved and applies to newly opened files. Vim and Nano are real
terminal editors managed by the daemon, with their own normal save/quit keys.
Mouse events are forwarded when the editor enables them. On macOS the system
`nano` can be Pico, which has Nano-style shortcuts. This Mac’s Pico did not
enable mouse reporting in validation; standard mode and Vim provide verified
mouse editing. You can install GNU Nano or
point a custom command at your preferred executable.

Settings live in the OS user configuration directory under `orkestar/tui.json`.
The panel shows the exact path. Set `ORKESTAR_TUI_CONFIG` to use another file.
For another terminal editor:

```json
{
  "editor": "custom",
  "command": ["nvim", "-c", "set mouse=a"],
  "max_panes": 16,
  "syntax": true
}
```

The selected file path is appended as one argument. Commands execute directly,
without shell interpolation. Missing editor executables produce a visible error.
