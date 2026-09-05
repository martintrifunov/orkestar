package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSavePreservesModeAndRejectsAgentChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "code.go")
	if err := os.WriteFile(path, []byte("before\n"), 0755); err != nil {
		t.Fatal(err)
	}
	d, err := Open(root, "code.go")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Save("after λ\n"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0755 {
		t.Fatal("file mode changed")
	}
	if err = os.WriteFile(path, []byte("agent change\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = d.Save("overwrite\n"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "agent change\n" {
		t.Fatal("overwrote agent changes")
	}
}
func TestCreateAndRejectUnsafeFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(root, "link"))
	if _, err := Open(root, "link/secret"); err == nil {
		t.Fatal("outside symlink accepted")
	}
	os.WriteFile(filepath.Join(root, "binary"), []byte{0, 1, 2}, 0600)
	if _, err := Open(root, "binary"); err == nil {
		t.Fatal("binary accepted")
	}
	d, err := Open(root, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(d.Path, []byte("created by agent"), 0644)
	if err = d.Save("mine"); !errors.Is(err, ErrConflict) {
		t.Fatal("overwrote newly created file")
	}
	d, err = Open(root, "another.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Save("new\n"); err != nil {
		t.Fatal(err)
	}
}
