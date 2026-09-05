# Orkestar handoff — 2026-09-05

Not committed on purpose. Delete it once you have read it.

## Start here

Repository `/Users/maco/repos/orkestar`, branch `main`. Read `AGENTS.md`, then
`docs/architecture.md`, `docs/roadmap.md` and the records in `docs/decisions/`.
`README.md` is now a real front page and is a good five-minute orientation.

**State right now**

- `HEAD` is `120e398 Center the README wordmark`. The working tree is clean.
- **Two commits are not pushed**: `0be4e4c` (resize repeat and focus fixes) and
  `120e398` (centred wordmark). Everything before them is on `origin/main`.
- CI is green on `origin/main` at `1fc839a`. The two unpushed commits have not
  been through CI yet.
- `./orkestar` is built from `120e398`. It is Git-ignored.

The user pushes themselves. Do not push without being asked.

## What this session did

Twenty commits. In rough order:

1. **Two scroll bugs.** The editor and diff panes bounced at the edges because
   every non-up wheel event counted as scroll-down, including horizontal
   trackpad events. Separately, the review pane emitted raw tabs, which the
   outer terminal expanded past the pane width and wrapped, pushing every later
   row and the footer down. `fitPane` now strips tabs for every pane type.
2. **Nested split panes** (ADR 0005). A binary split tree replaced the flat
   four-pane grid. `Ctrl+b v`/`s` always open a new shell beside or below the
   focused pane, splits nest, closing collapses onto the sibling, and
   `max_panes` in `tui.json` (default 16) refuses rather than replaces.
3. **Syntax highlighting** (ADR 0006). `internal/syntax` wraps chroma behind
   rune offsets and hex colours, about 300 languages, in Orkestar's own palette.
   Lexing runs as a background command so typing never waits.
4. **Tasks became usable from the sidebar.** There was no way to create a task
   from the UI at all, so the panel was permanently empty. `c` creates, `d`
   opens the task diff and reviewer verdict, which was unreachable dead code,
   `m` completes with visible progress, `x` cancels, `w` toggles the worktree,
   `t` assigns.
5. **A live file viewer.** `Ctrl+b f`, right edge, mirrors the sidebar, closed by
   default, follows the workspace while open.
6. **Session and agent lifecycle.** Nothing could stop a session before, and
   finished ones accumulated forever. `X` stops or clears, `i` interrupts, with
   matching `terminal.stop|remove` and `agent.stop|remove|interrupt` IPC methods
   and CLI commands.
7. **CI**, macOS and Linux, plus formatting, race and a cross-build job.
8. **Layout polish.** Pane titles in the border, `Ctrl+b z` zoom, split ratios
   with keyboard and drag resizing.
9. **README rewrite**, MIT licence, and the wordmark centred with
   `<pre align="center">`.
10. **Fixes from user testing.** Scrollback no longer opens on a stray wheel
    movement, resize arrows repeat without re-arming the prefix, focus is
    exclusive between sidebar, panes and viewer, and files opened from the
    viewer land below the focused pane at full width.

## What is left

In the order the user last agreed:

1. **Run the live agent matrix.** `python3 scripts/live-agents.py --adapters
   claude-code`, then `codex`. The harness exists and has never been run,
   because it spends real tokens on the logged-in account. Ask first. Record the
   JSON summary in the table in `docs/validation.md`, which says "not yet run"
   for all three. OpenCode has no credentials and needs `opencode auth login`.
2. **MCP client registry**, health checks, policy and audit middleware, per-agent
   tool exposure and serialised mutation routing. Roadmap M4. The user has
   deferred this twice; it blocks all game-engine work in M5.
3. **Mouse selection inside a terminal pane.** The only unshipped item from the
   layout bucket.
4. **Profile full-frame rendering under sustained output.** Never measured.

Nothing else in the roadmap is open outside M4 and M5.

## Things that will bite you

- **Rebuilding does not upgrade a running daemon.** A deliberate
  `orkestar daemon stop` ends the work it holds. Do not restart the user's
  daemon as a routine build step. Use isolated temporary daemons, as the scripts
  and tests do.
- **`cmp` is the wrong way to check whether `./orkestar` is stale.** Go stamps
  the commit hash and a `+dirty` flag into the binary, so a build made before a
  commit always differs byte-for-byte even when the code is identical. Check
  `go version -m ./orkestar` and compare the `mod` line to `git rev-parse HEAD`.
  I twice reported the binary as stale when only the stamp had changed.
- **Unix socket paths are capped near 104 characters on macOS.** Test daemons
  must put their socket under `/tmp`, not under a long scratch directory.
- **Anything rendered into a pane must not contain a raw tab**, or the outer
  terminal re-wraps the row and shifts the whole frame.
- **Attaching to a terminal is a race.** Output already on the daemon's screen
  arrives in the attach reply, not as a later event. A test that watches only
  events passes on macOS and fails on Linux. This was the CI failure; see
  `82d82f3`.
- Docker is available and `golang:1.27` runs the full Linux suite in about a
  minute. Use it before blaming CI for a platform difference.
- macOS `/usr/bin/nano` is Pico and does not enable mouse reporting.

## Verifying a change

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./internal/tui
go build -o ./orkestar ./cmd/orkestar
```

CI runs the same on both platforms. The suite needs no network and no installed
agent CLIs. `docs/validation.md` records what has actually been exercised and
carefully separates fixture coverage from installed-CLI coverage; keep that
distinction honest.
