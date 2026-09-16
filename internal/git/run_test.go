//go:build !windows

package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit puts a script named git at the front of PATH so runGit's stream
// handling can be observed without a real repository.
func fakeGit(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", dir)
}

func TestRunGitKeepsStderrOutOfParsedOutput(t *testing.T) {
	fakeGit(t, "#!/bin/sh\nprintf 'STDOUT-ONLY\\n'\nprintf 'STDERR-NOISE\\n' >&2\nexit 0\n")

	output, err := runGit(t.Context(), "status")
	if err != nil {
		t.Fatalf("runGit: %v", err)
	}
	if strings.Contains(output, "STDERR-NOISE") {
		t.Fatalf("stderr leaked into parsed output: %q", output)
	}
	if !strings.Contains(output, "STDOUT-ONLY") {
		t.Fatalf("stdout missing from parsed output: %q", output)
	}
}

func TestRunGitFoldsStderrIntoTheError(t *testing.T) {
	fakeGit(t, "#!/bin/sh\nprintf 'STDERR-NOISE\\n' >&2\nexit 1\n")

	if _, err := runGit(t.Context(), "status"); err == nil {
		t.Fatal("expected runGit to report the failure")
	} else if !strings.Contains(err.Error(), "STDERR-NOISE") {
		t.Fatalf("error lost git's own diagnostic: %v", err)
	}
}
