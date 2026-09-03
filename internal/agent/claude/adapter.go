// Package claude implements the Claude Code interactive agent adapter. It
// launches the installed `claude` executable inside a PTY and adapts it to
// the internal/agent contracts; there is no managed/SDK mode yet.
package claude

import (
	"context"
	"errors"
	"fmt"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/ptysession"
)

const defaultExecutable = "claude"

// Adapter launches Claude Code interactively in a PTY.
type Adapter struct {
	executable string
}

// New returns an Adapter that runs the given executable. An empty
// executable defaults to "claude" resolved from PATH. Tests can point this
// at a fixture script instead of a real Claude Code installation.
func New(executable string) *Adapter {
	if executable == "" {
		executable = defaultExecutable
	}
	return &Adapter{executable: executable}
}

func (a *Adapter) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		Name:                "claude-code",
		SupportsInteractive: true,
		SupportsManaged:     false,
		SupportsPrompt:      true,
		SupportsInterrupt:   true,
		SupportsResume:      false,
	}
}

func (a *Adapter) Launch(ctx context.Context, options agent.LaunchOptions) (agent.Session, error) {
	if options.Mode != agent.ModeInteractive {
		return nil, fmt.Errorf("claude adapter: mode %q is not supported", options.Mode)
	}
	if options.ResumeSessionID != "" {
		return nil, errors.New("claude adapter: resume is not supported yet")
	}
	session, err := ptysession.Launch("claude", a.executable, options)
	if err != nil {
		return nil, fmt.Errorf("claude adapter: %w", err)
	}
	return session, nil
}
