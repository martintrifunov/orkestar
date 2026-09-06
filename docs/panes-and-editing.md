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
