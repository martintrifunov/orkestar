package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func writeCast(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.cast")
	body := `{"version":2,"width":40,"height":10,"timestamp":0}` + "\n" +
		`[0.5,"o","hello-replay\r\n"]` + "\n" +
		`[1.5,"o","second-line\r\n"]` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	return path
}

func TestReplayPanePlaysAnAsciicast(t *testing.T) {
	pane := newReplayPane(writeCast(t), 40, 10)
	if pane.err != nil {
		t.Fatalf("load recording: %v", pane.err)
	}
	if len(pane.events) != 2 {
		t.Fatalf("expected two events, got %d", len(pane.events))
	}
	if strings.Contains(pane.Render(), "hello-replay") {
		t.Fatal("output was drawn before its time")
	}
	pane.seek(2)
	rendered := pane.Render()
	if !strings.Contains(rendered, "hello-replay") || !strings.Contains(rendered, "second-line") {
		t.Fatalf("replay did not draw the recorded output:\n%s", rendered)
	}

	pane.toggle()
	if !pane.playing {
		t.Fatal("toggle did not start playback")
	}
	pane.step(10 * time.Second)
	if pane.playing {
		t.Fatal("playback did not stop at the end")
	}
}

func TestReplayPaneRejectsAnotherVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.cast")
	if err := os.WriteFile(path, []byte(`{"version":1,"width":40,"height":10}`+"\n"), 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	pane := newReplayPane(path, 40, 10)
	if pane.err == nil {
		t.Fatal("a version this client cannot play should be reported")
	}
}

func TestRecordingsOverlayOpensAReplayPane(t *testing.T) {
	path := writeCast(t)
	model := Model{snapshot: sampleSnapshot(), width: 160, height: 42}
	model.snapshot.Artifacts = append(model.snapshot.Artifacts, workflow.Artifact{
		ID: "artifact_rec", TaskID: "task_1", Kind: workflow.ArtifactRecording,
		Label: "run log", Path: path,
	})

	updated, _ := model.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'R'})
	model = updated.(Model)
	if !model.recordingsOpen {
		t.Fatal("ctrl+b R did not open the recordings overlay")
	}
	if !strings.Contains(model.recordingsView(), "run log") {
		t.Fatalf("the recording is not listed:\n%s", model.recordingsView())
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if model.embedded == nil || model.embedded.replay == nil {
		t.Fatal("enter did not open a replay pane")
	}
	if label := model.paneLabel(model.embedded); label != "run log" {
		t.Fatalf("the pane is labelled %q", label)
	}
}
