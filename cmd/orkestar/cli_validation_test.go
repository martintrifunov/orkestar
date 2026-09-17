package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/machine"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

func TestTerminalReadRejectsNegativeLines(t *testing.T) {
	var paths runtimepath.Paths
	err := runTerminal(paths, []string{"read", "term_1", "--lines", "-5"})
	if err == nil || !strings.Contains(err.Error(), "--lines") {
		t.Fatalf("expected a --lines error, got %v", err)
	}
}

func TestAgentListAndReloadRejectTrailingArgsWithoutDaemon(t *testing.T) {
	var paths runtimepath.Paths
	// Must fail before starting a daemon for junk args.
	if err := runAgent(paths, []string{"list", "extra"}); err == nil {
		t.Fatal("expected agent list with trailing args to fail")
	}
	if err := runAgent(paths, []string{"reload", "extra"}); err == nil {
		t.Fatal("expected agent reload with trailing args to fail")
	}
}

func TestMachineAddAcceptsEqualsForm(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORKESTAR_MACHINES_FILE", dir+"/machines.json")
	var paths runtimepath.Paths
	if err := runMachine(paths, []string{"add", "user@example.com", "--label=ci-box", "--remote-session=work"}); err != nil {
		t.Fatalf("machine add with = flags: %v", err)
	}
}

func TestParseTerminalSend(t *testing.T) {
	id, text, enter, err := parseTerminalSend([]string{"term_1", "--enter", "hello", "world"})
	if err != nil || id != "term_1" || text != "hello world" || !enter {
		t.Fatalf("flag form: got %q %q %v %v", id, text, enter, err)
	}
	// After `--` everything is literal text, including `--enter` itself.
	id, text, enter, err = parseTerminalSend([]string{"term_1", "--", "--enter"})
	if err != nil || id != "term_1" || text != "--enter" || enter {
		t.Fatalf("separator form: got %q %q %v %v", id, text, enter, err)
	}
	if _, _, _, err := parseTerminalSend([]string{"term_1"}); err == nil {
		t.Fatal("expected missing text to fail")
	}
}

func TestMachineRemoveCleansLayout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORKESTAR_MACHINES_FILE", filepath.Join(dir, "machines.json"))
	paths := runtimepath.Paths{Directory: filepath.Join(dir, "runtime")}
	if err := runMachine(paths, []string{"add", "user@example.com", "--label", "box"}); err != nil {
		t.Fatalf("machine add: %v", err)
	}
	catalog, err := machine.Load(os.Getenv("ORKESTAR_MACHINES_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	listed := catalog.List()
	if len(listed) != 1 {
		t.Fatalf("expected one machine, got %#v", listed)
	}
	layoutDir := filepath.Join(paths.Directory, "machines", listed[0].ID)
	if err := os.MkdirAll(layoutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layoutDir, "layout.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMachine(paths, []string{"remove", listed[0].ID}); err != nil {
		t.Fatalf("machine remove: %v", err)
	}
	if _, err := os.Stat(layoutDir); !os.IsNotExist(err) {
		t.Fatalf("the removed machine's layout survived: %v", err)
	}
}
