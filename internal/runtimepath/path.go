package runtimepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const runtimeDirectoryEnvironment = "ORKESTAR_RUNTIME_DIR"

type Paths struct {
	Directory string
	Socket    string
	Log       string
}

// Resolve returns the paths for the default session.
func Resolve() (Paths, error) { return ResolveSession("") }

// ResolveSession returns the paths for a named session, which is a separate
// daemon with its own socket, database and log.
//
// One daemon per machine assumes one piece of work at a time. Two projects
// that should not share a board — different repositories, different agents,
// different tasks — have no way to be told apart otherwise, and a name is the
// smallest thing that does it.
func ResolveSession(session string) (Paths, error) {
	if err := validSession(session); err != nil {
		return Paths{}, err
	}
	directory := os.Getenv(runtimeDirectoryEnvironment)
	if directory == "" {
		cacheDirectory, err := os.UserCacheDir()
		if err != nil {
			return Paths{}, fmt.Errorf("find user cache directory: %w", err)
		}
		directory = filepath.Join(cacheDirectory, "orkestar")
	}

	directory, err := filepath.Abs(directory)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve runtime directory: %w", err)
	}
	// The default session keeps the original path, so an existing daemon and
	// its database are exactly where they were.
	if session != "" {
		directory = filepath.Join(directory, "sessions", session)
	}

	return Paths{
		Directory: directory,
		Socket:    filepath.Join(directory, "orkestar.sock"),
		Log:       filepath.Join(directory, "daemon.log"),
	}, nil
}

func Ensure(paths Paths) error {
	if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	return nil
}

// validSession rejects a name that would escape the runtime directory or
// collide with the layout. It reaches here from a command line and, in a
// remote session, from another machine.
func validSession(session string) error {
	if session == "" {
		return nil
	}
	if strings.ContainsAny(session, `/\`) || strings.ContainsAny(session, "\x00") {
		return fmt.Errorf("session name %q cannot be a path", session)
	}
	if session == "." || session == ".." {
		return fmt.Errorf("session name %q cannot be a path", session)
	}
	if strings.TrimSpace(session) != session || strings.TrimSpace(session) == "" {
		return fmt.Errorf("session name %q cannot begin or end with whitespace", session)
	}
	return nil
}
