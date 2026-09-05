# ADR 0006: Syntax highlighting in the standard editor

- Status: accepted
- Date: 2026-09-05

## Context

The standard editor rendered every file in one foreground color. Users edit
source and configuration files in it, and the review pane beside it already
colors diffs, so uncolored source looked out of place. Covering "every popular
language" means hundreds of grammars, which is not something to hand-write, and
the colors have to belong to Orkestar's existing palette rather than import a
third-party theme that clashes with the gold accent and the diff colors.

## Decision

Lexing lives in `internal/syntax`, which wraps `github.com/alecthomas/chroma/v2`
and exposes only rune offsets and hex colors. The TUI never sees a lexer type,
so the grammar library can be replaced without touching rendering, in keeping
with the rule that domain packages consume small Orkestar-owned interfaces.
Chroma supplies 297 lexers, including Go, Python, TypeScript, Rust, YAML, TOML,
JSON, Bash, SQL, Dockerfile, Terraform and Markdown. The language is chosen by
filename first and by content analysis second, so a shebang script with no
extension is still recognized. Plain text and unrecognized content are returned
uncolored rather than as an error: color is an enhancement and never blocks
opening a file.

The palette is Orkestar's own. Keywords use the accent gold, and strings, values
and function names reuse the green, salmon and blue the review pane already
uses for additions, deletions and hunk headers. Four muted hues in the same
family cover token kinds the rest of the interface has no color for: teal for
types, peach for numbers and escapes, violet for builtins and constants, slate
for operators and punctuation. Comments use the interface dim gray. Ordinary
identifiers stay at the default foreground, because coloring every name makes
source noisier rather than clearer.

Lexing costs roughly one millisecond per kilobyte, and more for some languages,
so it cannot run while rendering. It runs as a Bubble Tea command on a
background goroutine and returns its spans as a message. The view keeps the
previous colors until the result lands, so only the line being edited can
briefly show colors one keystroke old, and the spans are clamped to each line
so a stale result can never overrun a shortened line. One lex runs at a time;
a version counter drops results an edit has already superseded and schedules a
fresh pass instead. Text above 256 KiB is not lexed at all.

Highlighting is on by default and can be turned off with `h` in the editor
settings panel, or `"syntax": false` in `tui.json`. The toggle applies to
already-open buffers, not only to files opened afterwards. The status line
names the detected language, which doubles as confirmation that highlighting
is working.

## Consequences

The executable grows about 3.8 MB, from 21.3 MB to 25.1 MB, because chroma
embeds its lexer definitions. That is the price of broad language coverage in
one static binary and is accepted.

Colors are computed for the whole document even though only a screenful is
shown, because lexer state carries across lines and a window cannot be lexed
correctly on its own. Highlighting is per-file and syntactic only: there is no
semantic analysis, no language server and no cross-file knowledge. The review
pane still colors diffs by line kind rather than by language.
