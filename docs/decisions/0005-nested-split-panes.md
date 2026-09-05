# ADR 0005: Nested split panes with a bounded pane count

- Status: accepted
- Date: 2026-09-05

## Context

The first multi-pane layout was a flat list with one global orientation flag:
two columns, a 2×2 grid, or a vertical stack, capped at four panes. `Ctrl+b v`
and `Ctrl+b s` only created a shell when a single pane was open; otherwise they
rearranged the existing panes. Opening a fifth pane silently replaced the
focused attachment. Users expected each split to create a pane, and a stacked
split followed by a side-by-side split to yield three panes, as in tmux.

## Decision

The client layout is a binary split tree. Leaves hold panes; internal nodes
record side-by-side or stacked orientation and divide their rectangle in half,
with the second child taking the remainder so children always cover the parent
exactly. The tree is authoritative: pane rectangles, rendering joins, cursor
position, mouse hit testing and the PTY sizes sent to the daemon all derive from
the same rectangles. Focus cycling follows the in-order leaf sequence.

`Ctrl+b v` and `Ctrl+b s` always open a new daemon-owned shell and insert it
beside or below the focused pane. The target leaf and orientation are captured
when the key is pressed, before the asynchronous launch completes, so a focus
change during startup cannot misplace the new pane. If the target closes in the
meantime the pane lands beside the focused pane instead. A failed launch clears
the request. Only one split launch is in flight at a time.

Panes opened without a split key (agents, sessions, review, editors, `Ctrl+b n`)
split the focused leaf along its longer visual edge, treating a cell as about
twice as tall as it is wide. Closing a leaf collapses its parent onto the
sibling, which inherits the space; focus moves to that sibling's first pane.
Dirty standard editors keep their close protection.

The pane count is bounded by `max_panes` in `tui.json`, default 16, clamped to
1 through 64. Opening or splitting beyond the limit is refused with a visible
notice. A late attachment that arrives once the limit is reached is closed on
the client side only; the process keeps running in the daemon and stays listed
under Sessions. Nothing replaces an open pane silently.

When any leaf would be narrower than 24 cells or shorter than 7 rows, the client
shows only the focused pane at full size and says so in the header and footer.
Hidden panes keep their attachments; `F6` and `Ctrl+b o` cycle through them.

## Consequences

Layout state remains client-side and ephemeral; the daemon knows nothing about
splits and gains no new IPC. Splits are always equal halves; there is no ratio
adjustment, drag-to-resize, zoom toggle or pane swapping yet. Deep one-sided
splitting reaches the minimum size quickly and triggers the focused-pane
fallback rather than producing unusable boxes.
