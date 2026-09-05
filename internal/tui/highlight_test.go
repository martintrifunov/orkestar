package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/martintrifunov/orkestar/internal/files"
	"github.com/martintrifunov/orkestar/internal/syntax"
)

// lex runs the editor's background highlight synchronously.
func lex(t *testing.T, e *textEditor) {
	t.Helper()
	cmd := e.highlight()
	if cmd == nil {
		t.Fatal("no highlight was scheduled")
	}
	msg, ok := cmd().(highlightedMsg)
	if !ok {
		t.Fatal("highlight did not return its own message")
	}
	e.applyHighlight(msg)
}

// deliver runs a command and applies any highlight results it produces,
// including through a batch, the way the program loop would.
func deliver(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case highlightedMsg:
		deliver(t, msg.editor.applyHighlight(msg))
	case tea.BatchMsg:
		for _, next := range msg {
			deliver(t, next)
		}
	}
}

func editorFor(t *testing.T, name, text string) *textEditor {
	t.Helper()
	e := newTextEditor(&files.Document{Path: name, Text: text})
	e.columns, e.rows = 80, 24
	return e
}

// colored is the exact styled run the renderer emits for a token, built with
// the same style the editor uses so the assertion is not tied to an escape
// sequence spelling.
func colored(color, text string) string { return syntaxStyles[color].Render(text) }

func TestEditorColorsCodeAndNamesTheLanguage(t *testing.T) {
	cases := []struct {
		name, text string
		want       map[string]string
	}{
		{"main.go", "// note\npackage main\nvar s = \"hi\"\n", map[string]string{
			"// note": syntax.Comment, "package": syntax.Keyword, "\"hi\"": syntax.String,
		}},
		{"c.yaml", "# note\nname: orkestar\nport: 8080\n", map[string]string{
			"# note": syntax.Comment, "name": syntax.Function, "8080": syntax.Number,
		}},
		{"c.toml", "# note\nport = 8080\ns = \"v\"\n", map[string]string{
			"# note": syntax.Comment, "8080": syntax.Number, "\"v\"": syntax.String,
		}},
		{"d.json", "{\"k\": 1, \"on\": true}\n", map[string]string{
			"\"k\"": syntax.Function, "1": syntax.Number, "true": syntax.Constant,
		}},
		{"r.sh", "# note\nNAME=x\necho \"$NAME\"\n", map[string]string{
			"# note": syntax.Comment, "NAME": syntax.Variable,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := editorFor(t, c.name, c.text)
			lex(t, e)
			out := e.Render()
			if e.language == "" || !strings.Contains(out, "· "+e.language) {
				t.Fatalf("status line does not name the language: %q", e.language)
			}
			for token, color := range c.want {
				if !strings.Contains(out, colored(color, token)) {
					t.Errorf("%q was not rendered in %s", token, color)
				}
			}
		})
	}
}

func TestPlainTextIsNotColored(t *testing.T) {
	e := editorFor(t, "notes.txt", "just some words\nand more\n")
	if cmd := e.highlight(); cmd != nil {
		e.applyHighlight(cmd().(highlightedMsg))
	}
	out := e.Render()
	if e.language != "" || len(e.spans) != 0 {
		t.Fatalf("plain text was highlighted as %q", e.language)
	}
	body := strings.Join(strings.Split(out, "\n")[1:3], "\n")
	if body != ansi.Strip(body) {
		t.Fatalf("plain text carries color: %q", body)
	}
}

func TestHighlightingChangesOnlyColorNotLayout(t *testing.T) {
	text := "package main\n\nfunc run(v string) int {\n\treturn len(v) // λ note\n}\n"
	plain := editorFor(t, "main.go", text)
	plain.syntax = false
	colorful := editorFor(t, "main.go", text)
	lex(t, colorful)

	// The status line names the language only when highlighting is on, so
	// compare the document body rather than the whole frame.
	body := func(e *textEditor) string {
		lines := strings.Split(ansi.Strip(e.Render()), "\n")
		return strings.Join(lines[1:len(lines)-1], "\n")
	}
	if body(plain) != body(colorful) {
		t.Fatalf("highlighting changed the text:\nplain:\n%s\ncolored:\n%s", body(plain), body(colorful))
	}
	for _, e := range []*textEditor{plain, colorful} {
		e.cursor = e.index(3, 8)
	}
	px, py, _ := plain.Cursor()
	cx, cy, _ := colorful.Cursor()
	if px != cx || py != cy {
		t.Fatalf("cursor moved with highlighting: (%d,%d) vs (%d,%d)", px, py, cx, cy)
	}
	// A click must land on the same rune with or without color.
	plain.click(20, 4, false)
	colorful.click(20, 4, false)
	if plain.cursor != colorful.cursor {
		t.Fatalf("click landed differently: %d vs %d", plain.cursor, colorful.cursor)
	}
	if strings.Contains(colorful.Render(), "\t") {
		t.Fatal("a tab reached the pane and would wrap the row")
	}
}

func TestSelectionOverridesSyntaxColor(t *testing.T) {
	e := editorFor(t, "main.go", "package main\n")
	lex(t, e)
	e.anchor, e.cursor = 0, len("package")
	out := e.Render()
	if !strings.Contains(out, selectedStyle.Render("package")) {
		t.Fatalf("selection is not highlighted as selected:\n%q", out)
	}
	if strings.Contains(out, colored(syntax.Keyword, "package")) {
		t.Fatal("syntax color still covers the selected text")
	}
}

func TestColorsStayAlignedWhenScrolledSideways(t *testing.T) {
	e := editorFor(t, "main.go", "var padding = 1234567890 // aligned\nfunc later() {}\n")
	lex(t, e)
	e.left = 27
	out := e.Render()
	if !strings.Contains(out, colored(syntax.Comment, " aligned")) {
		t.Fatalf("comment lost its color after horizontal scroll:\n%q", out)
	}
	if strings.Contains(ansi.Strip(out), "padding") {
		t.Fatal("scrolled-off text is still rendered")
	}
	// The second line is shorter than the scroll offset and must render empty
	// rather than reusing the first line's colors.
	if strings.Contains(out, colored(syntax.Keyword, "func")) {
		t.Fatal("a line shorter than the scroll offset rendered its text")
	}
}

func TestStaleHighlightsAreDiscardedAndSupersededOnesRerun(t *testing.T) {
	e := editorFor(t, "main.go", "package main\n")
	lex(t, e)
	current := e.spansVersion

	// Only one lex runs at a time.
	e.replace("x")
	first := e.highlight()
	if first == nil {
		t.Fatal("an edit did not schedule a highlight")
	}
	if e.highlight() != nil {
		t.Fatal("a second lex was scheduled while one was in flight")
	}
	// An edit during that lex makes its result stale, so applying it must ask
	// for another pass rather than leave the colors behind.
	e.replace("y")
	again := e.applyHighlight(first().(highlightedMsg))
	if again == nil {
		t.Fatal("a superseded result did not schedule a fresh lex")
	}
	e.applyHighlight(again().(highlightedMsg))
	if e.spansVersion != e.version {
		t.Fatalf("colors never caught up: spans %d, text %d", e.spansVersion, e.version)
	}

	// A result that a newer one already replaced is dropped.
	fresh := e.spans
	e.applyHighlight(highlightedMsg{e, current - 1, syntax.Result{Language: "Stale", Lines: [][]syntax.Span{{{Start: 0, End: 1, Color: syntax.Invalid}}}}})
	if e.language == "Stale" || &fresh[0] != &e.spans[0] {
		t.Fatal("an out-of-date result overwrote current colors")
	}
}

func TestStaleSpansCannotOverrunAShrunkenLine(t *testing.T) {
	e := editorFor(t, "main.go", "var longIdentifierName = \"a long string literal\"\n")
	lex(t, e)
	// Delete most of the line but keep the colors from the longer version.
	e.anchor, e.cursor = 3, len(e.text)-1
	e.replace("")
	out := e.Render()
	if strings.Contains(out, "longIdentifierName") {
		t.Fatal("deleted text is still rendered")
	}
	if body := strings.Split(ansi.Strip(out), "\n")[1]; strings.TrimSpace(body) != "1 │var" {
		t.Fatalf("stale colors corrupted the shortened line: %q", body)
	}
}

func TestSyntaxSettingTogglesOpenEditors(t *testing.T) {
	m := New(nil, t.TempDir())
	m.width, m.height = 140, 40
	t.Setenv("ORKESTAR_TUI_CONFIG", t.TempDir()+"/tui.json")
	e := editorFor(t, "main.go", "package main\n")
	p := m.localPane("main.go", t.TempDir(), e)
	p.editor = e
	lex(t, e)
	if !strings.Contains(e.Render(), colored(syntax.Keyword, "package")) {
		t.Fatal("setup did not color the buffer")
	}
	m.settingsOpen = true
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'h'})
	m = updated.(Model)
	if m.settings.syntaxEnabled() || m.settingsOpen {
		t.Fatal("h did not turn highlighting off")
	}
	if out := e.Render(); strings.Contains(out, colored(syntax.Keyword, "package")) || strings.Contains(out, "· Go") {
		t.Fatal("open editor kept its colors after the setting was turned off")
	}
	if readSettings().syntaxEnabled() {
		t.Fatal("the choice was not saved")
	}
	m.settingsOpen = true
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'h'})
	m = updated.(Model)
	if !m.settings.syntaxEnabled() || cmd == nil {
		t.Fatal("h did not turn highlighting back on")
	}
	deliver(t, cmd)
	if !strings.Contains(e.Render(), colored(syntax.Keyword, "package")) {
		t.Fatal("colors did not come back")
	}
}

func TestHighlightedPaneStaysInsideItsBox(t *testing.T) {
	m := Model{width: 120, height: 30}
	e := editorFor(t, "main.go", "package main\n\nfunc run() {\n\tvar s = \"a very long string literal that runs past the pane edge for sure indeed yes\"\n}\n")
	p := m.localPane("main.go", t.TempDir(), e)
	p.editor = e
	lex(t, e)
	for _, line := range strings.Split(ansi.Strip(m.renderPanes()), "\n") {
		if ansi.StringWidth(line) > m.width || strings.Contains(line, "\t") {
			t.Fatalf("rendered row is %d cells wide: %q", ansi.StringWidth(line), line)
		}
	}
}
