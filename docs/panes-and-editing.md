# Panes and editing

Press and release **Ctrl+b**, then press the action key. Each action needs its
own prefix. The footer changes while a prefix is pending. In sidebar focus,
`v`, `s` and `o` also work directly. Typing these letters in an agent terminal
continues to type into that agent.

| Action | Shortcut |
| --- | --- |
| Side-by-side/grid layout; open a second shell if there is one pane | Ctrl+b, v |
| Stacked layout; open a second shell if there is one pane | Ctrl+b, s |
| Focus the next open pane | F6 or Ctrl+b, o |
| Open another shell | Ctrl+b, n |
| Open agent picker | Ctrl+b, a |
| Open workspace/task worktree review | Ctrl+b, d |
| Find/open/create a file | Ctrl+b, e |
| Choose editor mode | Ctrl+b, comma |
| Focus sidebar | Ctrl+b, Tab |
| Close focused pane | Ctrl+b, q |
| Explicitly discard a dirty standard editor | Ctrl+b, x |

There are at most four panes. A narrow window shows the focused pane and a hint
to enlarge the terminal; F6 still cycles hidden panes. Click any pane to focus it.

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
  "command": ["nvim", "-c", "set mouse=a"]
}
```

The selected file path is appended as one argument. Commands execute directly,
without shell interpolation. Missing editor executables produce a visible error.
