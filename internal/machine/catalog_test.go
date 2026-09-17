package machine_test

import (
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/machine"
)

func TestCatalogAddRenameAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "machines.json")
	catalog, err := machine.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.List()) != 0 {
		t.Fatalf("expected an empty catalog, got %#v", catalog.List())
	}

	added, err := catalog.Add("Build machine", "workbox", "agents")
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == "" || !added.Enabled || added.Label != "Build machine" {
		t.Fatalf("unexpected machine: %#v", added)
	}
	if _, err := catalog.Add("duplicate", "workbox", "agents"); err == nil {
		t.Fatal("expected a duplicate target to be rejected")
	}
	if _, err := catalog.Add("", "", ""); err == nil {
		t.Fatal("expected an empty host to be rejected")
	}
	if err := catalog.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := machine.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	listed := reloaded.List()
	if len(listed) != 1 || listed[0].Host != "workbox" || listed[0].Session != "agents" || !listed[0].Enabled {
		t.Fatalf("catalog did not round-trip: %#v", listed)
	}

	renamed, err := reloaded.Rename(added.ID, "Build box")
	if err != nil || renamed.Label != "Build box" {
		t.Fatalf("rename: %#v %v", renamed, err)
	}
	if _, err := reloaded.Rename(added.ID, "  "); err == nil {
		t.Fatal("expected an empty label to be rejected")
	}

	disabled, err := reloaded.SetEnabled(added.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable: %#v %v", disabled, err)
	}

	if err := reloaded.Remove(added.ID); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 0 {
		t.Fatalf("machine was not removed: %#v", reloaded.List())
	}
	if err := reloaded.Remove(added.ID); err == nil {
		t.Fatal("expected removing an unknown machine to fail")
	}
}

func TestCatalogDefaultsLabelToHost(t *testing.T) {
	catalog, err := machine.Load(filepath.Join(t.TempDir(), "machines.json"))
	if err != nil {
		t.Fatal(err)
	}
	added, err := catalog.Add("", "user@example.com:2222", "")
	if err != nil {
		t.Fatal(err)
	}
	if added.Label != "user@example.com:2222" {
		t.Fatalf("expected the label to default to the host, got %q", added.Label)
	}
}

func TestCatalogAddTrimsHost(t *testing.T) {
	catalog, err := machine.Load(filepath.Join(t.TempDir(), "machines.json"))
	if err != nil {
		t.Fatal(err)
	}
	added, err := catalog.Add("box", "  workbox  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if added.Host != "workbox" {
		t.Fatalf("the host was not trimmed: %q", added.Host)
	}
	if _, err := catalog.Add("duplicate", "workbox", ""); err == nil {
		t.Fatal("a padded duplicate should be rejected once trimmed")
	}
}

func TestCatalogListIsSortedByLabel(t *testing.T) {
	catalog, err := machine.Load(filepath.Join(t.TempDir(), "machines.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Add("zebra", "z", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Add("alpha", "a", ""); err != nil {
		t.Fatal(err)
	}
	listed := catalog.List()
	if len(listed) != 2 || listed[0].Label != "alpha" || listed[1].Label != "zebra" {
		t.Fatalf("catalog not sorted by label: %#v", listed)
	}
}
