package runtimepath_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

// The default session keeps the path it always had, so an existing daemon and
// its database stay exactly where they were.
func TestDefaultSessionIsUnchanged(t *testing.T) {
	t.Setenv("ORKESTAR_RUNTIME_DIR", t.TempDir())
	plain, err := runtimepath.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	named, err := runtimepath.ResolveSession("")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if plain != named {
		t.Fatalf("the empty session differs from the default: %+v vs %+v", plain, named)
	}
}

// A named session is a separate daemon: its own socket, database and log.
func TestNamedSessionsAreSeparate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ORKESTAR_RUNTIME_DIR", root)

	first, err := runtimepath.ResolveSession("api")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	second, err := runtimepath.ResolveSession("web")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	def, _ := runtimepath.Resolve()

	for _, pair := range [][2]string{
		{first.Socket, second.Socket},
		{first.Socket, def.Socket},
		{first.Log, second.Log},
	} {
		if pair[0] == pair[1] {
			t.Fatalf("two sessions share %q", pair[0])
		}
	}
	if !strings.HasPrefix(first.Directory, root) {
		t.Fatalf("a session escaped the runtime directory: %q", first.Directory)
	}
	if filepath.Base(first.Directory) != "api" {
		t.Fatalf("unexpected session directory %q", first.Directory)
	}
}

// The name arrives from a command line and, in a remote session, from another
// machine, so it cannot be a path.
func TestSessionNamesCannotEscape(t *testing.T) {
	t.Setenv("ORKESTAR_RUNTIME_DIR", t.TempDir())
	for _, name := range []string{"../elsewhere", "..", ".", "a/b", `a\b`, " padded", "padded "} {
		if _, err := runtimepath.ResolveSession(name); err == nil {
			t.Errorf("the name %q was accepted", name)
		}
	}
}
