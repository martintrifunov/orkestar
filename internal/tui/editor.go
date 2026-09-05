package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/martintrifunov/orkestar/internal/files"
	"github.com/martintrifunov/orkestar/internal/syntax"
)

// syntaxStyles is built once from the fixed palette so rendering a line does
// not allocate a style per colored run.
var syntaxStyles = func() map[string]lipgloss.Style {
	styles := make(map[string]lipgloss.Style, len(syntax.Colors()))
	for _, color := range syntax.Colors() {
		styles[color] = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	}
	return styles
}()

// highlightedMsg carries a finished background lex back to the UI goroutine.
type highlightedMsg struct {
	editor  *textEditor
	version int
	result  syntax.Result
}

type editState struct {
	text           []rune
	cursor, anchor int
}
type textEditor struct {
	searching                                bool
	query                                    string
	doc                                      *files.Document
	text                                     []rune
	cursor, anchor, top, left, columns, rows int
	undo, redo                               []editState
	status                                   string
	dragging                                 bool

	// version counts text mutations. spansVersion records which version the
	// colors were lexed from, so a stale background result can be discarded
	// and a superseded one can be relexed.
	highlighting bool
	syntax       bool
	version      int
	spansVersion int
	language     string
	spans        [][]syntax.Span
}

func newTextEditor(d *files.Document) *textEditor {
	return &textEditor{doc: d, text: []rune(d.Text), anchor: -1, columns: 60, rows: 20, syntax: true, spansVersion: -1}
}

// highlight lexes the document off the UI goroutine. Lexing costs roughly a
// millisecond per kilobyte, far too much to run while rendering, so the view
// keeps the previous colors until the new ones arrive: only the line being
// edited can briefly show colors one keystroke old. One lex runs at a time.
func (e *textEditor) highlight() tea.Cmd {
	if !e.syntax || e.highlighting || e.spansVersion == e.version {
		return nil
	}
	e.highlighting = true
	editor, version, name, text := e, e.version, e.doc.Path, string(e.text)
	return func() tea.Msg { return highlightedMsg{editor, version, syntax.Highlight(name, text)} }
}

// applyHighlight stores a background result unless a newer one already landed.
func (e *textEditor) applyHighlight(msg highlightedMsg) tea.Cmd {
	e.highlighting = false
	if msg.version >= e.spansVersion {
		e.spans = msg.result.Lines
		e.language = msg.result.Language
		e.spansVersion = msg.version
	}
	return e.highlight()
}

// setSyntax turns highlighting on or off for this open buffer.
func (e *textEditor) setSyntax(on bool) tea.Cmd {
	e.syntax = on
	if !on {
		e.spans, e.language, e.spansVersion = nil, "", e.version
		return nil
	}
	e.spansVersion = -1
	return e.highlight()
}
func (e *textEditor) dirty() bool { return string(e.text) != e.doc.Text }
func (e *textEditor) title() string {
	mark := ""
	if e.dirty() {
		mark = " *"
	}
	return e.doc.Path + mark
}
func (e *textEditor) snapshot() editState {
	return editState{append([]rune(nil), e.text...), e.cursor, e.anchor}
}
func (e *textEditor) bounds() (int, int) {
	if e.anchor < 0 {
		return e.cursor, e.cursor
	}
	return min(e.anchor, e.cursor), max(e.anchor, e.cursor)
}
func (e *textEditor) selected() string { a, b := e.bounds(); return string(e.text[a:b]) }
func (e *textEditor) replace(s string) {
	if len(string(e.text))+len(s) > files.MaxSize {
		e.status = "File exceeds editor size limit"
		return
	}
	e.undo = append(e.undo, e.snapshot())
	if len(e.undo) > 100 {
		e.undo = e.undo[1:]
	}
	e.redo = nil
	a, b := e.bounds()
	r := []rune(s)
	next := append([]rune(nil), e.text[:a]...)
	next = append(next, r...)
	next = append(next, e.text[b:]...)
	e.text = next
	e.cursor = a + len(r)
	e.anchor = -1
	e.status = ""
	e.version++
	e.reveal()
}
func (e *textEditor) rowCol() (int, int) {
	row, col := 0, 0
	for _, r := range e.text[:e.cursor] {
		if r == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return row, col
}
func (e *textEditor) index(row, col int) int {
	at := 0
	for i, line := range strings.Split(string(e.text), "\n") {
		r := []rune(line)
		if i == row {
			return at + min(col, len(r))
		}
		at += len(r) + 1
	}
	return len(e.text)
}
func (e *textEditor) reveal() {
	row, col := e.rowCol()
	if row < e.top {
		e.top = row
	}
	if row >= e.top+max(1, e.rows-2) {
		e.top = row - max(1, e.rows-2) + 1
	}
	if col < e.left {
		e.left = col
	}
	if col >= e.left+max(1, e.columns-7) {
		e.left = col - max(1, e.columns-7) + 1
	}
}
func (e *textEditor) Render() string {
	lines := strings.Split(string(e.text), "\n")
	a, b := e.bounds()
	at := 0
	var out []string
	help := "Ctrl+S save · Ctrl+Z undo · Ctrl+F find"
	if e.searching {
		help = "Find: " + e.query + "▏ · Enter next · Esc close"
	}
	out = append(out, dimStyle.Render(help))
	for row, line := range lines {
		r := []rune(line)
		if row >= e.top && len(out) < e.rows-1 {
			content := e.renderLine(row, r, a, b, at)
			out = append(out, fmt.Sprintf("%4d │", row+1)+ansi.Truncate(content, max(1, e.columns-6), ""))
		}
		at += len(r) + 1
	}
	for len(out) < e.rows-1 {
		out = append(out, "")
	}
	row, col := e.rowCol()
	status := e.status
	if status == "" {
		status = fmt.Sprintf("Ln %d, Col %d", row+1, col+1)
		if e.language != "" {
			status += " · " + e.language
		}
	}
	out = append(out, dimStyle.Render(status))
	return strings.Join(out, "\n")
}

// renderLine styles one line, grouping neighbouring runes that share a color
// and selection state into a single escape sequence.
func (e *textEditor) renderLine(row int, r []rune, selectionStart, selectionEnd, offset int) string {
	colors := make([]string, len(r))
	if row < len(e.spans) {
		for _, s := range e.spans[row] {
			// Colors can be one keystroke behind the text while a background
			// lex is in flight, so clamp spans to the line instead of trusting
			// their offsets.
			for i := max(0, s.Start); i < min(s.End, len(r)); i++ {
				colors[i] = s.Color
			}
		}
	}
	selected := func(col int) bool { return offset+col >= selectionStart && offset+col < selectionEnd }
	var out strings.Builder
	for start := max(0, e.left); start < len(r); {
		end := start + 1
		for end < len(r) && colors[end] == colors[start] && selected(end) == selected(start) {
			end++
		}
		segment := displayRunes(r[start:end])
		switch style, ok := syntaxStyles[colors[start]]; {
		case selected(start):
			out.WriteString(selectedStyle.Render(segment))
		case ok:
			out.WriteString(style.Render(segment))
		default:
			out.WriteString(segment)
		}
		start = end
	}
	return out.String()
}

// displayRunes substitutes runes the pane cannot show literally. A tab would
// be expanded to the outer terminal's stop past the pane width and wrap the
// row; control characters would move the cursor.
func displayRunes(r []rune) string {
	var b strings.Builder
	for _, c := range r {
		switch {
		case c == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(c):
			b.WriteRune('·')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
func (e *textEditor) Cursor() (int, int, bool) {
	row, col := e.rowCol()
	line := strings.Split(string(e.text), "\n")[row]
	r := []rune(line)
	display := strings.ReplaceAll(string(r[min(e.left, len(r)):col]), "\t", " ")
	return 6 + ansi.StringWidth(display), 1 + row - e.top, true
}
func (e *textEditor) Resize(w, h int) {
	e.columns = w
	e.rows = h
	e.top = min(e.top, e.maxTop())
}
func (e *textEditor) maxTop() int {
	return max(0, len(strings.Split(string(e.text), "\n"))-max(1, e.rows-2))
}
func (e *textEditor) scroll(delta int) {
	e.dragging = false
	e.top = max(0, min(e.maxTop(), e.top+delta))
}
func (e *textEditor) Input(b []byte) { e.replace(string(b)) }
func (e *textEditor) Paste(s string) {
	if e.searching {
		e.query += s
		return
	}
	e.replace(s)
}
func (e *textEditor) Navigation(code rune, mod int) {
	e.key(tea.KeyPressMsg{Code: code, Mod: tea.KeyMod(mod)})
}
func (e *textEditor) Close() error { return nil }
func (e *textEditor) key(k tea.KeyPressMsg) tea.Cmd {
	if e.searching {
		switch k.String() {
		case "esc":
			e.searching = false
		case "enter":
			e.findNext()
		case "backspace":
			r := []rune(e.query)
			if len(r) > 0 {
				e.query = string(r[:len(r)-1])
			}
		default:
			if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
				e.query += k.Text
			} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
				e.query += string(k.Code)
			}
		}
		return nil
	}
	if k.String() == "ctrl+f" {
		e.searching = true
		e.query = ""
		return nil
	}

	switch k.String() {
	case "ctrl+s":
		if err := e.doc.Save(string(e.text)); err != nil {
			e.status = err.Error()
		} else {
			e.status = "Saved"
		}
		return nil
	case "ctrl+a":
		e.anchor = 0
		e.cursor = len(e.text)
		e.reveal()
		return nil
	case "ctrl+c":
		return tea.SetClipboard(e.selected())
	case "ctrl+x":
		s := e.selected()
		if s != "" {
			e.replace("")
		}
		return tea.SetClipboard(s)
	case "ctrl+v":
		return tea.ReadClipboard
	case "ctrl+z", "ctrl+y", "ctrl+shift+z":
		source, target := &e.undo, &e.redo
		if k.String() != "ctrl+z" {
			source, target = &e.redo, &e.undo
		}
		if len(*source) > 0 {
			*target = append(*target, e.snapshot())
			s := (*source)[len(*source)-1]
			*source = (*source)[:len(*source)-1]
			e.text = s.text
			e.cursor = s.cursor
			e.anchor = s.anchor
			e.version++
			e.reveal()
		}
		return nil
	}
	old := e.cursor
	row, col := e.rowCol()
	move := true
	switch k.Code {
	case tea.KeyLeft:
		e.cursor = max(0, e.cursor-1)
		if k.Mod&tea.ModCtrl != 0 {
			for e.cursor > 0 && unicode.IsSpace(e.text[e.cursor]) {
				e.cursor--
			}
			for e.cursor > 0 && !unicode.IsSpace(e.text[e.cursor-1]) {
				e.cursor--
			}
		}
	case tea.KeyRight:
		e.cursor = min(len(e.text), e.cursor+1)
		if k.Mod&tea.ModCtrl != 0 {
			for e.cursor < len(e.text) && !unicode.IsSpace(e.text[e.cursor]) {
				e.cursor++
			}
			for e.cursor < len(e.text) && unicode.IsSpace(e.text[e.cursor]) {
				e.cursor++
			}
		}
	case tea.KeyUp:
		e.cursor = e.index(max(0, row-1), col)
	case tea.KeyDown:
		e.cursor = e.index(row+1, col)
	case tea.KeyHome:
		e.cursor = e.index(row, 0)
		if k.Mod&tea.ModCtrl != 0 {
			e.cursor = 0
		}
	case tea.KeyEnd:
		e.cursor = e.index(row, 1<<30)
		if k.Mod&tea.ModCtrl != 0 {
			e.cursor = len(e.text)
		}
	case tea.KeyPgUp:
		e.cursor = e.index(max(0, row-max(1, e.rows-2)), col)
	case tea.KeyPgDown:
		e.cursor = e.index(row+max(1, e.rows-2), col)
	default:
		move = false
	}
	if move {
		if k.Mod&tea.ModShift != 0 {
			if e.anchor < 0 {
				e.anchor = old
			}
		} else {
			e.anchor = -1
		}
		e.reveal()
		return nil
	}
	switch k.Code {
	case tea.KeyBackspace:
		a, b := e.bounds()
		if a == b && e.cursor > 0 {
			e.anchor = e.cursor - 1
		}
		if e.anchor >= 0 {
			e.replace("")
		}
	case tea.KeyDelete:
		a, b := e.bounds()
		if a == b && e.cursor < len(e.text) {
			e.anchor = e.cursor + 1
		}
		if e.anchor >= 0 {
			e.replace("")
		}
	case tea.KeyEnter:
		e.replace("\n")
	case tea.KeyTab:
		e.replace("\t")
	default:
		if k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 && k.Text != "" {
			e.replace(k.Text)
		} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
			e.replace(string(k.Code))
		}
	}
	return nil
}
func (e *textEditor) click(x, y int, extend bool) {
	row := max(0, y-1) + e.top
	lines := strings.Split(string(e.text), "\n")
	row = min(row, len(lines)-1)
	r := []rune(lines[row])
	col := min(e.left, len(r))
	width := 0
	for col < len(r) && width < max(0, x-6) {
		w := ansi.StringWidth(string(r[col]))
		if r[col] == '\t' {
			w = 1
		}
		if width+w > max(0, x-6) {
			break
		}
		width += w
		col++
	}
	if extend {
		if e.anchor < 0 {
			e.anchor = e.cursor
		}
	} else {
		e.anchor = -1
	}
	e.cursor = e.index(row, col)
	e.reveal()
}

func (e *textEditor) findNext() {
	if e.query == "" {
		return
	}
	tail := string(e.text[e.cursor:])
	at := strings.Index(tail, e.query)
	start := e.cursor
	if at < 0 {
		tail = string(e.text)
		at = strings.Index(tail, e.query)
		start = 0
	}
	if at < 0 {
		e.status = "No match"
		return
	}
	start += len([]rune(tail[:at]))
	e.anchor = start
	e.cursor = start + len([]rune(e.query))
	e.status = ""
	e.reveal()
}
