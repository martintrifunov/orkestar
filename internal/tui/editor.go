package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/martintrifunov/orkestar/internal/files"
)

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
}

func newTextEditor(d *files.Document) *textEditor {
	return &textEditor{doc: d, text: []rune(d.Text), anchor: -1, columns: 60, rows: 20}
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
			var content strings.Builder
			for col, c := range r {
				if col >= e.left {
					s := string(c)
					if c == '\t' {
						s = " "
					}
					if unicode.IsControl(c) {
						s = "·"
					}
					if at+col >= a && at+col < b {
						s = selectedStyle.Render(s)
					}
					content.WriteString(s)
				}
			}
			out = append(out, fmt.Sprintf("%4d │", row+1)+ansi.Truncate(content.String(), max(1, e.columns-6), ""))
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
	}
	out = append(out, dimStyle.Render(status))
	return strings.Join(out, "\n")
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
