package tui

import (
	"bytes"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/martintrifunov/orkestar/internal/files"
)

type reviewFile struct{ path, status string }
type reviewPane struct {
	root                         string
	files                        []reviewFile
	selected, top, columns, rows int
	diff                         string
	err                          error
}
type reviewLoaded struct {
	pane   *embeddedTerminal
	review *reviewPane
}

func gitOutput(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "--no-pager"}, args...)...)
	var out reviewOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	b := out.Bytes()
	if out.truncated {
		return "", fmt.Errorf("review output exceeds 2 MiB; use a narrower file or external review tool")
	}
	if err != nil {
		return "", fmt.Errorf("git: %s", strings.TrimSpace(string(b)))
	}
	return string(b), nil
}
func loadReview(root string, selected int) *reviewPane {
	r := &reviewPane{root: root, columns: 60, rows: 20}
	raw, err := gitOutput(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		r.err = err
		return r
	}
	parts := strings.Split(raw, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		r.files = append(r.files, reviewFile{p[3:], p[:2]})
		if strings.ContainsAny(p[:2], "RC") {
			i++
		}
	}
	r.selected = min(selected, max(0, len(r.files)-1))
	r.loadFile()
	return r
}
func (r *reviewPane) loadFile() {
	r.top = 0
	r.err = nil
	r.diff = ""
	if len(r.files) == 0 {
		return
	}
	f := r.files[r.selected]
	if f.status == "??" {
		d, err := files.Open(r.root, f.path)
		if err != nil {
			r.err = err
			return
		}
		var b strings.Builder
		b.WriteString("@@ new file @@\n")
		for _, line := range strings.Split(d.Text, "\n") {
			b.WriteString("+" + line + "\n")
		}
		r.diff = b.String()
		return
	}
	base := "HEAD"
	if _, err := gitOutput(r.root, "rev-parse", "--verify", "HEAD"); err != nil {
		base = "--cached"
	}
	r.diff, r.err = gitOutput(r.root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", base, "--", f.path)
}
func (r *reviewPane) Render() string {
	out := []string{dimStyle.Render("[ / ] file · e edit · r refresh · wheel scroll")}
	if r.err != nil {
		return strings.Join(append(out, errorStyle.Render(r.err.Error())), "\n")
	}
	if len(r.files) == 0 {
		return strings.Join(append(out, "No uncommitted changes against HEAD."), "\n")
	}
	start := max(0, r.selected-1)
	for i := start; i < min(len(r.files), start+3); i++ {
		line := r.files[i].status + " " + r.files[i].path
		if i == r.selected {
			line = selectedStyle.Render(line)
		}
		out = append(out, line)
	}
	out = append(out, dimStyle.Render(fmt.Sprintf("File %d/%d · staged + unstaged vs HEAD", r.selected+1, len(r.files))))
	lines := strings.Split(r.diff, "\n")
	old, newLine := 0, 0
	var rendered []string
	for _, line := range lines {
		// Tabs would be expanded by the outer terminal past the pane width and
		// wrap, pushing every later row of the screen down. Render them as spaces.
		line = strings.ReplaceAll(ansi.Strip(line), "\t", "    ")
		prefix := "           "
		color := ""
		switch {
		case strings.HasPrefix(line, "@@"):
			fmt.Sscanf(line, "@@ -%d", &old)
			if at := strings.Index(line, " +"); at >= 0 {
				fmt.Sscanf(line[at:], " +%d", &newLine)
			}
			color = "#78A9E8"
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			color = "#D7A84B"
		case strings.HasPrefix(line, "+"):
			prefix = fmt.Sprintf("     %4d +", newLine)
			newLine++
			line = line[1:]
			color = "#82C997"
		case strings.HasPrefix(line, "-"):
			prefix = fmt.Sprintf("%4d      -", old)
			old++
			line = line[1:]
			color = "#EE9696"
		case strings.HasPrefix(line, " "):
			prefix = fmt.Sprintf("%4d %4d  ", old, newLine)
			old++
			newLine++
			line = line[1:]
		}
		line = prefix + line
		if color != "" {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(line)
		}
		rendered = append(rendered, line)
	}
	top := max(0, min(r.top, r.maxTop()))
	return strings.Join(append(out, rendered[top:min(len(rendered), top+max(1, r.rows-len(out)))]...), "\n")
}
func (r *reviewPane) Cursor() (int, int, bool) { return 0, 0, false }
func (r *reviewPane) Resize(w, h int)          { r.columns = w; r.rows = h; r.top = min(r.top, r.maxTop()) }
func (r *reviewPane) Input([]byte)             {}
func (r *reviewPane) Paste(string)             {}
func (r *reviewPane) Navigation(rune, int)     {}
func (r *reviewPane) Close() error             { return nil }
func (m Model) refreshReview(p *embeddedTerminal, selection int) tea.Cmd {
	return func() tea.Msg { return reviewLoaded{p, loadReview(p.root, selection)} }
}

type reviewOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *reviewOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 2*1024*1024 - b.Len()
	if len(p) > remaining {
		p = p[:max(0, remaining)]
		b.truncated = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *reviewOutput) Bytes() []byte { return b.buffer.Bytes() }
func (b *reviewOutput) Len() int      { return b.buffer.Len() }

func (r *reviewPane) maxTop() int {
	header := 2 + min(3, max(0, len(r.files)-max(0, r.selected-1)))
	return max(0, len(strings.Split(r.diff, "\n"))-max(1, r.rows-header))
}
func (r *reviewPane) scroll(delta int) { r.top = max(0, min(r.maxTop(), r.top+delta)) }
