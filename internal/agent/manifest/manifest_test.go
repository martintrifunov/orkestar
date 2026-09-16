package manifest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/manifest"
)

// fixtureExecutable is a stand-in agent CLI that records the arguments it was
// launched with, so a test can check what a manifest actually passed.
func fixtureExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func launchArguments(t *testing.T, descriptor manifest.Manifest, resume string) []string {
	t.Helper()
	adapter, err := manifest.New(descriptor)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	session, err := adapter.Launch(context.Background(), agent.LaunchOptions{
		Mode:            agent.ModeInteractive,
		Directory:       t.TempDir(),
		Columns:         80,
		Rows:            24,
		ResumeSessionID: resume,
		Environment:     []string{"ARGS_FILE=" + argsFile},
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, err := os.ReadFile(argsFile)
		if err == nil {
			text := strings.TrimRight(string(raw), "\n")
			if text == "" {
				return nil
			}
			return strings.Split(text, "\n")
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture never recorded its arguments")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLaunchPassesManifestAndResumeArguments(t *testing.T) {
	executable := fixtureExecutable(t)
	arguments := launchArguments(t, manifest.Manifest{
		Name:       "fixture",
		Executable: executable,
		Arguments:  []string{"--interactive"},
		Resume:     &manifest.Resume{Arguments: []string{"--resume", "{id}"}},
	}, "native-42")

	want := []string{"--interactive", "--resume", "native-42"}
	if strings.Join(arguments, " ") != strings.Join(want, " ") {
		t.Fatalf("expected %v, got %v", want, arguments)
	}
}

// A resume template with no placeholder is a subcommand, and the native ID is
// appended: codex resume <id>, not codex --resume=<id>.
func TestResumeWithoutPlaceholderAppendsTheID(t *testing.T) {
	arguments := launchArguments(t, manifest.Manifest{
		Name:       "fixture",
		Executable: fixtureExecutable(t),
		Resume:     &manifest.Resume{Arguments: []string{"resume"}},
	}, "native-7")

	want := []string{"resume", "native-7"}
	if strings.Join(arguments, " ") != strings.Join(want, " ") {
		t.Fatalf("expected %v, got %v", want, arguments)
	}
}

func TestResumeIsRefusedWithoutAResumeSection(t *testing.T) {
	adapter, err := manifest.New(manifest.Manifest{Name: "fixture", Executable: fixtureExecutable(t)})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	if _, err := adapter.Launch(context.Background(), agent.LaunchOptions{
		Mode: agent.ModeInteractive, ResumeSessionID: "native-1",
	}); err == nil {
		t.Fatal("expected resume to be refused without a resume section")
	}
}

func TestCapabilitiesFollowTheManifest(t *testing.T) {
	yes, no := true, false
	capabilities := manifest.Manifest{
		Name:       "fixture",
		Executable: "fixture",
		Resume:     &manifest.Resume{Arguments: []string{"--resume", "{id}"}},
	}.Capabilities()
	if !capabilities.SupportsInteractive || !capabilities.SupportsResume ||
		!capabilities.SupportsPrompt || !capabilities.SupportsInterrupt {
		t.Fatalf("unexpected defaults: %#v", capabilities)
	}
	if capabilities.SupportsManaged {
		t.Fatalf("a manifest is never managed: %#v", capabilities)
	}

	quiet := manifest.Manifest{Name: "quiet", Executable: "quiet", Prompt: &no, Interrupt: &yes}.Capabilities()
	if quiet.SupportsPrompt || !quiet.SupportsInterrupt || quiet.SupportsResume {
		t.Fatalf("unexpected overrides: %#v", quiet)
	}
}

func TestLoadRejectsTyposAndMissingFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{"name":"x","executable":"x","resumee":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Load(path); err == nil {
		t.Fatal("expected an unknown field to be rejected")
	}

	missing := filepath.Join(dir, "missing.json")
	if err := os.WriteFile(missing, []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Load(missing); err == nil {
		t.Fatal("expected a missing executable to be rejected")
	}
}

func TestLoadDirSkipsInvalidFilesAndSorts(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"b.json":      `{"name":"beta","executable":"beta"}`,
		"a.json":      `{"name":"alpha","executable":"alpha"}`,
		"broken.json": `{"name":"broken"}`,
		"notes.txt":   `not a manifest`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifests, err := manifest.LoadDir(dir)
	if err == nil {
		t.Fatal("expected the invalid manifest to be reported")
	}
	if len(manifests) != 2 || manifests[0].Name != "alpha" || manifests[1].Name != "beta" {
		t.Fatalf("unexpected manifests: %#v", manifests)
	}

	if manifests, err := manifest.LoadDir(filepath.Join(dir, "absent")); err != nil || manifests != nil {
		t.Fatalf("a missing directory should not be an error: %#v %v", manifests, err)
	}
}
