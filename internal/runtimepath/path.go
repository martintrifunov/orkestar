package runtimepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const runtimeDirectoryEnvironment = "ORKESTAR_RUNTIME_DIR"

// agentManifestDirectoryEnvironment overrides where declarative agent
// manifests are read from, which is what tests and unusual installs need.
const agentManifestDirectoryEnvironment = "ORKESTAR_AGENT_MANIFESTS_DIR"

// machineCatalogEnvironment overrides the saved-machine catalog path.
const machineCatalogEnvironment = "ORKESTAR_MACHINES_FILE"

// policyFileEnvironment overrides the user-level permission policy path.
const policyFileEnvironment = "ORKESTAR_POLICY_FILE"

// AgentManifestDirectory is where declarative agent manifests live. It is
// configuration rather than runtime state, so it sits under the user config
// directory rather than beside the socket.
func AgentManifestDirectory() (string, error) {
	if directory := os.Getenv(agentManifestDirectoryEnvironment); directory != "" {
		return directory, nil
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(configDirectory, "orkestar", "agents"), nil
}

// MachineCatalogPath is where saved SSH machines live, beside the agent
// manifests. It is configuration, not runtime state.
func MachineCatalogPath() (string, error) {
	if path := os.Getenv(machineCatalogEnvironment); path != "" {
		return path, nil
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(configDirectory, "orkestar", "machines.json"), nil
}

// PolicyFilePath is the user-level permission policy, the fallback beneath a
// workspace's own .orkestar/policy.json. It is configuration, not runtime
// state, so it sits with the agent manifests.
func PolicyFilePath() (string, error) {
	if path := os.Getenv(policyFileEnvironment); path != "" {
		return path, nil
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(configDirectory, "orkestar", "policy.json"), nil
}

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
