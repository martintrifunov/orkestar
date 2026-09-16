package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

func TestResolveThemeFallsBackToDefault(t *testing.T) {
	if resolveTheme("").name != "orkestar" {
		t.Fatalf("an unnamed theme should resolve to the default")
	}
	if resolveTheme("nope").name != "orkestar" {
		t.Fatalf("an unknown theme should resolve to the default")
	}
	if resolveTheme("light").name != "light" {
		t.Fatalf("a named theme should resolve to itself")
	}
	if len(ThemeNames()) < 2 {
		t.Fatalf("expected at least two palettes to cycle, got %v", ThemeNames())
	}
}

func TestLightThemeRendersDifferently(t *testing.T) {
	dark := New(ipc.NewClient("solo"), t.TempDir())
	dark.snapshot.Agents = []daemon.Agent{{ID: "a1", Adapter: "claude-code", State: "working"}}

	light := dark
	light.theme = resolveTheme("light")
	if dark.renderAgents() == light.renderAgents() {
		t.Fatal("the two palettes render identically")
	}
}

func TestReadSettingsTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	if err := os.WriteFile(path, []byte(`{"theme":"light"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORKESTAR_TUI_CONFIG", path)
	if got := readSettings().Theme; got != "light" {
		t.Fatalf("theme was not read from tui.json: %q", got)
	}
}
