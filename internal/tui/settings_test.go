package tui

import (
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/agent"
)

// A manifest edited on disk only took effect after `orkestar agent reload`.
// The settings screen now offers the same action.
func TestReloadAdaptersFromSettings(t *testing.T) {
	adapter := agent.NewFakeAdapter(agent.Capabilities{
		Name: "fake", SupportsInteractive: true, SupportsPrompt: true,
	})
	client := startEmbeddedTestDaemon(t, adapter)
	m := New(client, t.TempDir())
	m.width, m.height = 140, 40
	m.settingsOpen = true

	if view := m.promptView(); !strings.Contains(view, "Reload agent adapters") {
		t.Fatalf("settings does not offer the reload:\n%s", view)
	}
	m, cmd := press(t, m, 'r')
	if cmd == nil || m.settingsOpen {
		t.Fatal("r did not leave settings to reload")
	}
	if !strings.Contains(m.notice, "Reloading") {
		t.Fatalf("the reload is invisible: %q", m.notice)
	}
	m = settle(t, m, cmd)
	if m.err != nil {
		t.Fatalf("reload failed: %v", m.err)
	}
	if !strings.Contains(m.notice, "Adapters reloaded") {
		t.Fatalf("reload was not reported: %q", m.notice)
	}
	found := false
	for _, capabilities := range m.snapshot.Adapters {
		if capabilities.Name == "fake" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the adapter is not in the refreshed snapshot: %+v", m.snapshot.Adapters)
	}
}
