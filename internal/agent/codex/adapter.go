package codex

import (
	"context"
	"fmt"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/hooks"
	"github.com/martintrifunov/orkestar/internal/agent/ptysession"
)

type Adapter struct{ executable string }

func New(executable string) *Adapter {
	if executable == "" {
		executable = "codex"
	}
	return &Adapter{executable}
}
func (a *Adapter) Capabilities() agent.Capabilities {
	return agent.Capabilities{Name: "codex", SupportsInteractive: true, SupportsPrompt: true, SupportsInterrupt: true, SupportsResume: true}
}
func (a *Adapter) Launch(ctx context.Context, o agent.LaunchOptions) (agent.Session, error) {
	if o.Mode != agent.ModeInteractive {
		return nil, fmt.Errorf("codex supports interactive mode only")
	}
	if o.HookCommand != "" {
		o.Arguments = append(o.Arguments, hooks.Codex(o.HookCommand)...)
	}
	if o.ResumeSessionID != "" {
		o.Arguments = append(o.Arguments, "resume", o.ResumeSessionID)
	}
	return ptysession.Launch("codex", a.executable, o)
}
