// Package syntax turns source text into per-line colored spans. It keeps the
// lexer dependency behind one small interface: callers receive plain rune
// offsets and hex colors and never see a lexer type, so the implementation can
// be replaced without touching the UI.
package syntax

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// The palette matches Orkestar's interface colors: the accent gold, and the
// green, salmon and blue already used by the review pane, plus four muted
// hues in the same family for token kinds the rest of the UI has no color for.
// Every color is chosen for a dark background.
const (
	Keyword     = "#D7A84B" // accent gold, shared with the focused pane border
	String      = "#82C997" // review-pane addition green
	Variable    = "#EE9696" // review-pane deletion salmon
	Function    = "#78A9E8" // review-pane hunk-header blue
	Comment     = "#777777" // interface dim
	Type        = "#7FD1C4" // teal
	Number      = "#E8B98A" // peach
	Constant    = "#C7A0E8" // violet
	Punctuation = "#A7B0BC" // slate
	Invalid     = "#FF6B6B" // interface error red
)

// Colors lists every color Highlight can return, so a renderer can build its
// styles once instead of per span.
func Colors() []string {
	return []string{Keyword, String, Variable, Function, Comment, Type, Number, Constant, Punctuation, Invalid}
}

// MaxSize is the largest text that is highlighted. Lexing is linear in the
// input, so above this the cost outweighs the benefit and the text is shown
// unhighlighted rather than stalling repeated re-lexing while typing.
const MaxSize = 256 * 1024

// Span is a run of runes on one line that shares a color. Offsets are rune
// offsets within the line, Start inclusive and End exclusive.
type Span struct {
	Start, End int
	Color      string
}

// Result is the highlighting of one document. Language is the detected
// language's display name, empty when nothing matched. Lines holds one entry
// per line of the input, each with non-overlapping spans in ascending order;
// runes not covered by a span use the default foreground.
type Result struct {
	Language string
	Lines    [][]Span
}

// Highlight lexes text as the language indicated by name, falling back to
// content analysis when the name is unknown. Plain text, unrecognized content
// and oversized input all return an empty result rather than an error: syntax
// color is an enhancement and never blocks showing the file.
func Highlight(name, text string) Result {
	if len(text) > MaxSize {
		return Result{}
	}
	lexer := lexers.Match(filepath.Base(name))
	if lexer == nil {
		lexer = lexers.Analyse(head(text))
	}
	if lexer == nil {
		return Result{}
	}
	language := lexer.Config().Name
	if language == "plaintext" || language == "fallback" {
		return Result{}
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return Result{Language: language}
	}
	lines := make([][]Span, strings.Count(text, "\n")+1)
	row, column := 0, 0
	for _, token := range iterator.Tokens() {
		color := colorFor(token.Type)
		for i, piece := range strings.Split(token.Value, "\n") {
			if i > 0 {
				row++
				column = 0
			}
			width := len([]rune(piece))
			if width > 0 && color != "" && row < len(lines) {
				lines[row] = add(lines[row], Span{column, column + width, color})
			}
			column += width
		}
	}
	return Result{Language: language, Lines: lines}
}

// add appends a span, merging it into the previous one when they touch and
// share a color. Adjacent token types often map to the same color, and merging
// keeps the rendered escape sequences down.
func add(spans []Span, s Span) []Span {
	if n := len(spans); n > 0 && spans[n-1].End == s.Start && spans[n-1].Color == s.Color {
		spans[n-1].End = s.End
		return spans
	}
	return append(spans, s)
}

// head returns the leading portion used for content analysis, cut at a line
// boundary so a shebang or opening tag is intact and the cost stays bounded.
func head(text string) string {
	if len(text) <= 4096 {
		return text
	}
	cut := text[:4096]
	if at := strings.LastIndexByte(cut, '\n'); at > 0 {
		return cut[:at+1]
	}
	return cut
}

// colorFor maps a token to a palette color, empty for ordinary identifiers and
// text. Specific types are checked before their categories, so that string
// escapes, interpolation and preprocessor lines can differ from the plain
// string or comment they sit inside.
func colorFor(t chroma.TokenType) string {
	// Only NameFunction, NameBuiltin and NameVariable have their own
	// subcategory; every other Name subtype collapses to Name, so those are
	// matched exactly here.
	switch t {
	case chroma.LiteralStringEscape, chroma.LiteralStringInterpol:
		return Number
	case chroma.CommentPreproc, chroma.CommentPreprocFile:
		return Constant
	case chroma.KeywordType:
		return Type
	case chroma.KeywordConstant:
		return Constant
	case chroma.NameTag, chroma.NameAttribute, chroma.NameProperty:
		return Function
	case chroma.NameClass, chroma.NameNamespace, chroma.NameException:
		return Type
	case chroma.NameConstant, chroma.NameDecorator, chroma.NameEntity:
		return Constant
	case chroma.Name, chroma.NameOther, chroma.NameLabel:
		// Ordinary identifiers stay at the default foreground; coloring them
		// makes source noisier rather than clearer.
		return ""
	}
	switch t.SubCategory() {
	case chroma.LiteralNumber:
		return Number
	case chroma.LiteralString:
		return String
	case chroma.NameFunction:
		return Function
	case chroma.NameBuiltin:
		return Constant
	case chroma.NameVariable:
		return Variable
	}
	switch t.Category() {
	case chroma.Comment:
		return Comment
	case chroma.Keyword:
		return Keyword
	case chroma.Literal:
		return String
	case chroma.Operator, chroma.Punctuation:
		return Punctuation
	case chroma.Error:
		return Invalid
	}
	return ""
}
