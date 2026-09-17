// Package manifest turns a JSON description of a coding-agent CLI into an
// internal/agent.Adapter, so adding an agent is configuration rather than a
// new Go package. A manifest is deliberately small: a name, an executable, the
// arguments that launch it, and how to resume one. Structured lifecycle still
// comes from hooks where an adapter has them; a manifest gets a PTY session
// and whatever a process can observe.
package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/ptysession"
)

// Resume describes how a CLI resumes a native session. Arguments is a template
// run through with the native session ID appended as a final argument unless
// "{id}" appears somewhere in it.
type Resume struct {
	Arguments []string `json:"arguments"`
}

// DetectionRule maps a substring of the pane's output to a lifecycle state,
// for a CLI with no hook channel. Rules are tried in order.
type DetectionRule struct {
	State    string `json:"state" jsonschema:"one of working, waiting_input, waiting_permission, waiting_resource, ready"`
	Contains string `json:"contains"`
}

// Manifest is a declarative agent adapter.
type Manifest struct {
	Name        string   `json:"name"`
	Executable  string   `json:"executable"`
	Arguments   []string `json:"arguments,omitempty"`
	Description string   `json:"description,omitempty"`
	// Prompt and Interrupt default to true; a pointer distinguishes an absent
	// field from a deliberate false.
	Prompt    *bool           `json:"prompt,omitempty"`
	Interrupt *bool           `json:"interrupt,omitempty"`
	Resume    *Resume         `json:"resume,omitempty"`
	Detection []DetectionRule `json:"detection,omitempty"`
}

// Validate reports whether the manifest can produce a usable adapter.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Name) != m.Name {
		return errors.New("name is required and cannot have surrounding whitespace")
	}
	if strings.TrimSpace(m.Executable) == "" {
		return errors.New("executable is required")
	}
	if m.Resume != nil && len(m.Resume.Arguments) == 0 {
		return errors.New("resume.arguments cannot be empty when resume is set")
	}
	for _, rule := range m.Detection {
		if strings.TrimSpace(rule.Contains) == "" {
			// A blank substring matches every pane with a space in it and
			// would pin the agent in that rule's state; matching nothing
			// is never what a rule means.
			return errors.New("a detection rule needs a non-blank contains string")
		}
		switch agent.State(rule.State) {
		case agent.StateWorking, agent.StateWaitingInput, agent.StateWaitingPermission,
			agent.StateWaitingResource, agent.StateReady:
		default:
			return fmt.Errorf("detection rule %q has state %q, which is not a reportable state", rule.Contains, rule.State)
		}
	}
	return nil
}

// Capabilities reports what the manifest can do. A manifest is a PTY adapter:
// it is interactive, never managed, and supports prompt, interrupt and resume
// according to its fields.
func (m Manifest) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		Name:                m.Name,
		SupportsInteractive: true,
		SupportsManaged:     false,
		SupportsPrompt:      m.Prompt == nil || *m.Prompt,
		SupportsInterrupt:   m.Interrupt == nil || *m.Interrupt,
		SupportsResume:      m.Resume != nil,
	}
}

// resumeArguments expands the resume template for a native session ID.
func (r Resume) resumeArguments(id string) []string {
	arguments := make([]string, 0, len(r.Arguments)+1)
	replaced := false
	for _, argument := range r.Arguments {
		if strings.Contains(argument, "{id}") {
			argument = strings.ReplaceAll(argument, "{id}", id)
			replaced = true
		}
		arguments = append(arguments, argument)
	}
	if !replaced {
		arguments = append(arguments, id)
	}
	return arguments
}

// Adapter launches the manifest's CLI through the shared PTY session.
type Adapter struct{ manifest Manifest }

// normalized trims the fields a user may have padded, so validation, the
// adapter and duplicate detection all see the same value.
func (m Manifest) normalized() Manifest {
	m.Name = strings.TrimSpace(m.Name)
	m.Executable = strings.TrimSpace(m.Executable)
	return m
}

// New validates a manifest and returns an adapter for it.
func New(m Manifest) (*Adapter, error) {
	m = m.normalized()
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("agent manifest %q: %w", m.Name, err)
	}
	return &Adapter{manifest: m}, nil
}

func (a *Adapter) Capabilities() agent.Capabilities { return a.manifest.Capabilities() }

// Detections exposes the manifest's detection rules so the daemon can infer a
// lifecycle state from the pane when the CLI reports none.
func (a *Adapter) Detections() []agent.Detection {
	detections := make([]agent.Detection, 0, len(a.manifest.Detection))
	for _, rule := range a.manifest.Detection {
		detections = append(detections, agent.Detection{State: agent.State(rule.State), Contains: rule.Contains})
	}
	return detections
}

func (a *Adapter) Launch(ctx context.Context, options agent.LaunchOptions) (agent.Session, error) {
	if options.Mode != agent.ModeInteractive {
		return nil, fmt.Errorf("agent %q runs interactively only", a.manifest.Name)
	}
	arguments := append([]string{}, a.manifest.Arguments...)
	if options.ResumeSessionID != "" {
		if a.manifest.Resume == nil {
			// Refused rather than ignored: starting fresh when the caller asked
			// to resume loses whatever the old session held.
			return nil, fmt.Errorf("agent %q sessions cannot be resumed", a.manifest.Name)
		}
		arguments = append(arguments, a.manifest.Resume.resumeArguments(options.ResumeSessionID)...)
	}
	options.Arguments = append(arguments, options.Arguments...)
	return ptysession.Launch(a.manifest.Name, a.manifest.Executable, options)
}

// Load reads one manifest. Unknown fields are rejected so a typo in a key is
// reported rather than silently ignored.
func Load(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode agent manifest %s: %w", path, err)
	}
	manifest = manifest.normalized()
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("agent manifest %s: %w", path, err)
	}
	return manifest, nil
}

// LoadDir reads every *.json manifest in dir, sorted by name. A missing
// directory is not an error: manifests are optional. Invalid files are skipped
// and returned together as one error, so one typo does not hide the rest. Two
// manifests claiming the same name are a problem too: the second is skipped
// rather than silently shadowing the first.
func LoadDir(dir string) ([]Manifest, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent manifest directory %s: %w", dir, err)
	}
	manifests := make([]Manifest, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	var problems []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		manifest, err := Load(filepath.Join(dir, entry.Name()))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if seen[manifest.Name] {
			problems = append(problems, fmt.Errorf("agent manifest %s: duplicate name %q", entry.Name(), manifest.Name))
			continue
		}
		seen[manifest.Name] = true
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(left, right int) bool { return manifests[left].Name < manifests[right].Name })
	return manifests, errors.Join(problems...)
}
