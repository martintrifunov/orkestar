package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// recordingSummary is one recording artifact with the task it belongs to, so
// the list says what the recording is of rather than only where the file is.
type recordingSummary struct {
	Artifact  workflow.Artifact
	TaskTitle string
}

// openRecordings shows the recordings the board knows about. The list is read
// from the snapshot the interface already has, so it opens without a call.
func (m *Model) openRecordings() {
	m.recordingsOpen = true
	m.recordingAt = 0
	m.recordings = nil
	for _, artifact := range m.snapshot.Artifacts {
		if artifact.Kind != workflow.ArtifactRecording {
			continue
		}
		title := ""
		for _, task := range m.snapshot.Tasks {
			if task.ID == artifact.TaskID {
				title = task.Title
			}
		}
		m.recordings = append(m.recordings, recordingSummary{Artifact: artifact, TaskTitle: title})
	}
}

func (m Model) recordingsView() string {
	var lines []string
	lines = append(lines, "Recordings", "")
	if len(m.recordings) == 0 {
		lines = append(lines, "No recordings yet. Start one with:")
		lines = append(lines, "  orkestar terminal record start <terminal-id> --task=<task-id>")
	} else {
		for index, recording := range m.recordings {
			row := fmt.Sprintf("%-24s %s", recording.Artifact.Label, recording.TaskTitle)
			if index == m.recordingAt {
				row = m.theme.selected.Render(row)
			}
			lines = append(lines, row)
			if index == m.recordingAt {
				lines = append(lines, m.theme.dim.Render("    "+recording.Artifact.Path))
			}
		}
	}
	lines = append(lines, "", "↑/↓ select · Enter replay · Esc close")
	return strings.Join(lines, "\n")
}

func (m Model) updateRecordings(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEscape:
		m.recordingsOpen = false
	case tea.KeyUp:
		m.recordingAt = max(0, m.recordingAt-1)
	case tea.KeyDown:
		if m.recordingAt+1 < len(m.recordings) {
			m.recordingAt++
		}
	case tea.KeyEnter:
		if m.recordingAt < 0 || m.recordingAt >= len(m.recordings) {
			return m, nil
		}
		return m.openReplay(m.recordings[m.recordingAt].Artifact)
	}
	return m, nil
}

// openReplay opens a recording as a pane. It reuses a replay pane already
// showing the same file rather than opening a second copy.
func (m Model) openReplay(artifact workflow.Artifact) (tea.Model, tea.Cmd) {
	if artifact.Path == "" {
		m.notice = "That recording has no file to replay."
		return m, nil
	}
	for _, pane := range m.visiblePanes() {
		if pane.replay != nil && pane.replay.path == artifact.Path {
			m.embedded = pane
			m.sidebarFocused = false
			m.recordingsOpen = false
			return m, nil
		}
	}
	if !m.roomForPane() {
		m.notice = "No room for a replay pane; close one first."
		return m, nil
	}
	_, _, columns, rows := m.contentArea()
	replay := newReplayPane(artifact.Path, columns-4, rows-3)
	replay.title = artifact.Label
	pane := m.localPane("Replay", "", replay)
	pane.replay = replay
	m.embedded = pane
	m.sidebarFocused = false
	m.recordingsOpen = false
	return m, nil
}
