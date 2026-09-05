package syntax

import (
	"strings"
	"testing"
)

// at returns the color covering a rune offset on a line, or "" for none.
func at(t *testing.T, r Result, row, column int) string {
	t.Helper()
	if row >= len(r.Lines) {
		t.Fatalf("line %d missing; %d lines highlighted", row, len(r.Lines))
	}
	for _, s := range r.Lines[row] {
		if column >= s.Start && column < s.End {
			return s.Color
		}
	}
	return ""
}

// colorOf returns the color of the first occurrence of want on a line.
func colorOf(t *testing.T, r Result, text, want string, row int) string {
	t.Helper()
	line := strings.Split(text, "\n")[row]
	at1 := strings.Index(line, want)
	if at1 < 0 {
		t.Fatalf("%q not on line %d", want, row)
	}
	return at(t, r, row, len([]rune(line[:at1])))
}

func TestPopularLanguagesGetTheExpectedColors(t *testing.T) {
	cases := []struct {
		name, text string
		want       map[string]string
	}{
		{"main.go", "package main\n// note\nfunc run() { x := \"s\" }\nconst n = 42\n", map[string]string{
			"package": Keyword, "// note": Comment, "run": Function, "\"s\"": String, "42": Number,
		}},
		{"config.yaml", "# note\nname: orkestar\nport: 8080\non: true\ns: \"q\"\n", map[string]string{
			"# note": Comment, "name": Function, "8080": Number, "true": Constant, "\"q\"": String,
		}},
		{"config.toml", "# note\n[server]\nname = \"orkestar\"\nport = 8080\non = true\n", map[string]string{
			"# note": Comment, "\"orkestar\"": String, "8080": Number, "true": Constant,
		}},
		{"data.json", "{\n\"name\": \"orkestar\",\n\"port\": 8080,\n\"on\": true\n}\n", map[string]string{
			"\"name\"": Function, "\"orkestar\"": String, "8080": Number, "true": Constant,
		}},
		{"run.sh", "#!/bin/sh\n# note\nNAME=x\necho \"$NAME\"\n", map[string]string{
			"#!/bin/sh": Constant, "# note": Comment, "NAME": Variable, "echo": Constant,
		}},
		{"app.py", "# note\nclass Widget:\n    def build(self):\n        return 'v'\n", map[string]string{
			"# note": Comment, "class": Keyword, "Widget": Type, "build": Function, "'v'": String,
		}},
		{"lib.rs", "// note\npub fn main() { let x: u32 = 1; }\n", map[string]string{
			"// note": Comment, "pub": Keyword, "main": Function, "u32": Type, "1": Number,
		}},
		{"app.ts", "// note\nexport const f = (a: number): string => `x`\n", map[string]string{
			"// note": Comment, "export": Keyword, "`x`": String,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Highlight(c.name, c.text)
			if r.Language == "" {
				t.Fatal("language not detected")
			}
			if len(r.Lines) != strings.Count(c.text, "\n")+1 {
				t.Fatalf("got %d lines, text has %d", len(r.Lines), strings.Count(c.text, "\n")+1)
			}
			for token, want := range c.want {
				row := -1
				for i, line := range strings.Split(c.text, "\n") {
					if strings.Contains(line, token) {
						row = i
						break
					}
				}
				if row < 0 {
					t.Fatalf("token %q not in the fixture", token)
				}
				if got := colorOf(t, r, c.text, token, row); got != want {
					t.Errorf("%q got %s, want %s", token, got, want)
				}
			}
		})
	}
}

func TestSpansStayInsideTheirLineAndDoNotOverlap(t *testing.T) {
	text := "package main\n\n// λ unicode comment\nfunc f() string { return \"λ ok\" }\n"
	r := Highlight("main.go", text)
	lines := strings.Split(text, "\n")
	for row, spans := range r.Lines {
		width := len([]rune(lines[row]))
		last := 0
		for _, s := range spans {
			if s.Start < last {
				t.Fatalf("line %d spans overlap or are unsorted: %+v", row, spans)
			}
			if s.Start < 0 || s.End > width || s.Start >= s.End {
				t.Fatalf("line %d span %+v outside a %d-rune line", row, s, width)
			}
			if s.Color == "" {
				t.Fatalf("line %d has an uncolored span %+v", row, s)
			}
			last = s.End
		}
	}
	// The comment contains a multi-byte rune, so byte offsets would be wrong.
	if got := colorOf(t, r, text, "unicode", 2); got != Comment {
		t.Fatalf("rune offsets are misaligned after a multi-byte rune: %s", got)
	}
}

func TestDetectionFallsBackToContentAndNeverFails(t *testing.T) {
	if r := Highlight("deploy", "#!/bin/bash\necho hi\n"); r.Language == "" {
		t.Fatal("shebang was not detected without an extension")
	}
	for _, c := range []struct{ name, text string }{
		{"notes.txt", "just words\n"},
		{"", ""},
		{"a.unknownext", "%%% not a language %%%\n"},
	} {
		if r := Highlight(c.name, c.text); len(r.Lines) != 0 {
			t.Fatalf("%q should not be highlighted, got %d lines", c.name, len(r.Lines))
		}
	}
	big := strings.Repeat("package main\n", MaxSize/13+1)
	if r := Highlight("main.go", big); r.Language != "" || r.Lines != nil {
		t.Fatal("oversized input must be skipped, not lexed")
	}
}

func TestAdjacentSpansOfOneColorAreMerged(t *testing.T) {
	r := Highlight("main.go", "// one long comment on a single line\n")
	if n := len(r.Lines[0]); n != 1 {
		t.Fatalf("a uniform comment produced %d spans, want 1: %+v", n, r.Lines[0])
	}
}

func TestPaletteIsComplete(t *testing.T) {
	known := map[string]bool{}
	for _, c := range Colors() {
		if len(c) != 7 || c[0] != '#' {
			t.Fatalf("palette entry %q is not a hex color", c)
		}
		known[c] = true
	}
	text := "package main\n// c\nimport \"fmt\"\ntype T struct{ n int }\nfunc f(v string) { fmt.Println(1, v, true) }\n"
	for _, spans := range Highlight("main.go", text).Lines {
		for _, s := range spans {
			if !known[s.Color] {
				t.Fatalf("Highlight returned %s, which Colors() does not list", s.Color)
			}
		}
	}
}
