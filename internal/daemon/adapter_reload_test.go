package daemon

import (
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/agent"
)

// A reload rebuilds the whole adapter set, so a manifest that was removed or
// changed stops being offered rather than lingering from the first load.
func TestReloadAdaptersReplacesTheSet(t *testing.T) {
	server := NewServer(filepath.Join(t.TempDir(), "socket"))
	server.RegisterAdapter(agent.NewFakeAdapter(agent.Capabilities{Name: "first", SupportsInteractive: true}))

	server.SetAdapterLoader(func() ([]agent.Adapter, error) {
		return []agent.Adapter{
			agent.NewFakeAdapter(agent.Capabilities{Name: "second", SupportsInteractive: true}),
			agent.NewFakeAdapter(agent.Capabilities{Name: "third", SupportsInteractive: true}),
		}, nil
	})

	capabilities, err := server.reloadAdapters()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(capabilities) != 2 || capabilities[0].Name != "second" || capabilities[1].Name != "third" {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}

	server.mu.RLock()
	_, firstKept := server.adapters["first"]
	_, secondThere := server.adapters["second"]
	server.mu.RUnlock()
	if firstKept {
		t.Fatal("a reload kept an adapter that the loader no longer returns")
	}
	if !secondThere {
		t.Fatal("a reload did not register the loaded adapter")
	}
}

func TestReloadAdaptersWithoutALoaderFailsClearly(t *testing.T) {
	server := NewServer(filepath.Join(t.TempDir(), "socket"))
	if _, err := server.reloadAdapters(); err == nil {
		t.Fatal("expected a reload without a loader to fail")
	}
}
