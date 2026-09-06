// Package grok runs the Grok CLI in a daemon-owned terminal.
package grok

import (
	"context"
	"fmt"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/ptysession"
)

// Adapter launches the grok CLI as an interactive session.
//
// It claims less than the Claude Code and Codex adapters do, and deliberately.
// Those two carry hook wiring that was written against the real CLIs and
// verified against them, which is what lets Orkestar know when they are
// working, waiting for input or waiting for a permission. Nothing here has
// been checked against a real grok, so rather than guess at flags and
// a hook contract, this runs the CLI and reports only what a PTY session can
// see on its own: it started, it stopped, or it died.
//
// The practical effect is that a Cursor session gets a pane, a task, an
// opening prompt and the whole workflow around it, while attention states and
// resume wait for someone with the CLI in front of them.
type Adapter struct{ executable string }

func New(executable string) *Adapter {
	if executable == "" {
		executable = "grok"
	}
	return &Adapter{executable}
}

func (a *Adapter) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		Name:                "grok",
		SupportsInteractive: true,
		SupportsPrompt:      true,
		SupportsInterrupt:   true,
	}
}

func (a *Adapter) Launch(ctx context.Context, o agent.LaunchOptions) (agent.Session, error) {
	if o.Mode != agent.ModeInteractive {
		return nil, fmt.Errorf("grok supports interactive mode only")
	}
	if o.ResumeSessionID != "" {
		// Refused rather than ignored: silently starting a fresh session when
		// the caller asked to resume one loses whatever it held.
		return nil, fmt.Errorf("grok sessions cannot be resumed yet")
	}
	return ptysession.Launch("grok", a.executable, o)
}
