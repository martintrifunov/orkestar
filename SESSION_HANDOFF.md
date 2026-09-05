# Orkestar session handoff — 2026-09-05 (third session)

## Read this first next session

Repository: `/Users/maco/repos/orkestar`. Branch: `main`. Nothing pushed.
Read `AGENTS.md`, this file, `docs/architecture.md`, `docs/roadmap.md` and the
ADRs under `docs/decisions/` before continuing.

The user prefers action without repeated permission questions. Do not stop or
restart their running daemon as a routine step: that ends managed work. Use
isolated temporary daemons for checks, as the scripts in `scripts/` do.

**Head:** `a91e635 Add a live authenticated agent validation harness`.
The worktree is clean apart from this file. The root `./orkestar` binary is
Git-ignored and was rebuilt after the last code change.

## What this session did

Five commits, each with its tests:

1. `d3ad290` — the previous session's uncommitted scroll fix, committed as its
   own change with `internal/tui/scroll_test.go`.
2. `aebaa63` — new bug the user found: scrolling the diff pane pushed the editor
   pane and the footer help down. The review pane emitted raw tab characters
   from diff hunks; the outer terminal expanded them to 8-column stops past the
   measured pane width and wrapped the row, adding physical rows. Tabs are now
   spaces in the review pane, and `fitPane` strips them for every pane type.
3. `b70636a` — nested split panes, replacing the flat grid. See below.
4. `0c76890` — real outer-PTY test for nested splits with actual keystrokes.
5. `a91e635` — `scripts/live-agents.py`, the authenticated live validation
   harness, plus documentation. **It has not been run.**

## Nested splits, as shipped (ADR 0005)

`internal/tui/splits.go` holds a binary split tree: leaves are panes, internal
nodes halve their rectangle side by side or stacked, second child takes the
remainder. The tree is authoritative for rectangles, rendering joins, cursor
placement, mouse hit testing and PTY sizes. `Model.panes`/`Model.stacked` are
gone, replaced by `Model.layout` and `Model.pendingSplit`.

- `Ctrl+b v`/`s` always launch a new daemon-owned shell beside/below the focused
  pane. The target and orientation are captured in `pendingSplit` before the
  async launch, so a focus change during startup cannot misplace the pane. A
  closed target falls back to the focused pane; a failed launch clears it; only
  one split launch is in flight at a time.
- Other new panes (agents, sessions, review, editors, `Ctrl+b n`) split the
  focused leaf along its longer visual edge.
- Closing collapses the parent onto the sibling, which inherits the space.
- `max_panes` in `tui.json`, default 16, clamped 1–64. Over the limit, opening
  is refused with a notice; a late attachment is closed client-side only and its
  process keeps running. Nothing is ever replaced silently.
- Leaves under 24×7 fall back to showing only the focused pane; hidden panes stay
  attached and F6/`Ctrl+b o` still cycle them.

Tests: `splits_test.go` (tree operations, exact tiling of nine panes with no
overlap, resize, collapse, split target surviving focus change/failure/close,
the configurable limit), `panes_test.go`, `documents_test.go` (mixed splits
through real daemon shells) and the new real-PTY test in `program_test.go`.

## Validation run this session

- `gofmt` clean, `go vet ./...`, `go test ./...`, `go test -race ./internal/tui`
  all passed on macOS arm64 after every commit.
- `go build -o ./orkestar ./cmd/orkestar` rebuilt after the last code change.
- The user has manually confirmed neither the scroll fix nor the tab fix in the
  real app yet; both were reported by them from the running binary.

## Next steps, in order

1. **Run the live matrix.** `python3 scripts/live-agents.py --adapters claude-code`
   (then `codex`). It spends real tokens on the logged-in account, so the user
   should agree first; this session offered and the run was declined. Record the
   JSON summary in the table in `docs/validation.md`, which currently says
   "not yet run" for every CLI. OpenCode has zero credentials
   (`opencode auth list`); it needs `opencode auth login` before it can be run.
   Claude Code is logged in via claude.ai and Codex via ChatGPT.
2. The user deferred these for a later session, explicitly out of scope now:
   macOS/Linux CI with race and real-PTY checks (roadmap M0), and the MCP client
   registry with health checks, policy/audit and serialized routing (M4).
3. Possible layout follow-ups, none requested: split ratios and drag resizing,
   pane titles, a zoom toggle, mouse selection.

## Notes that keep biting

- Replacing `./orkestar` does not upgrade a running daemon. A deliberate daemon
  stop ends its managed processes.
- The older in-memory daemon has no live migration into the SQLite store.
- Tabs: anything rendered into a pane must not contain a raw tab, or the outer
  terminal re-wraps the row and shifts the whole frame. `fitPane` now guards it.
- macOS `/usr/bin/nano` is Pico and did not enable mouse reporting in validation.
- The temporary OpenCode download at `/tmp/orkestar-tools/opencode` from an
  earlier session may be gone; a real OpenCode 1.18.29 is on PATH.
- No sub-agents were spawned. Follow the current session's delegation rules.
