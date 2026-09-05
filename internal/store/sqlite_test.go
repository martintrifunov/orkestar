package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSQLiteRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Save(context.Background(), []byte(`{"tasks":["one"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	data, err := db.Load(context.Background())
	if err != nil || string(data) != `{"tasks":["one"]}` {
		t.Fatalf("round trip: %s %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix mode bits; Go reports 0666 there. The metadata file
	// inherits the ACL of the user-private runtime directory instead.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("metadata permissions %v", info.Mode())
	}
}
func TestSQLiteRejectsUnknownVersion(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.db.Exec("INSERT INTO metadata VALUES(1,99,'{}')")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Load(context.Background()); err == nil {
		t.Fatal("future schema accepted")
	}
}
