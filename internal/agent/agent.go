// Package agent defines the contracts the daemon uses to launch, drive, and
// observe coding-agent adapters (Claude Code, OpenCode, and future tools)
// without assuming they all behave the same way.
package agent

import (
	"context"
	"time"

	"github.com/martintrifunov/orkestar/internal/pty"
)

// State is a coarse agent lifecycle state, as described in
// docs/architecture.md.
type State string

const (
	StateStarting          State = "starting"
	StateReady             State = "ready"
	StateWorking           State = "working"
	StateWaitingInput      State = "waiting_input"
	StateWaitingPermission State = "waiting_permission"
	StateWaitingResource   State = "waiting_resource"
	StateStopped           State = "stopped"
	StateCrashed           State = "crashed"
)

// Mode is how an adapter launches and drives the underlying agent process.
type Mode string

const (
	// ModeInteractive attaches the agent to a PTY the way a human would run
	// it in a terminal. Output and control flow through terminal semantics.
	ModeInteractive Mode = "interactive"
	// ModeManaged drives the agent through a structured channel, such as
	// streaming JSON or an SDK bridge, rather than raw terminal bytes.
	ModeManaged Mode = "managed"
)

// Capabilities describes what an adapter supports so the daemon and TUI can
// adapt rather than assume uniform behavior across agents.
type Capabilities struct {
	// Name identifies the adapter, e.g. "claude-code" or "opencode".
	Name string `json:"name"`
	// SupportsInteractive is true when the adapter can launch the agent in
	// an interactive PTY.
	SupportsInteractive bool `json:"supports_interactive"`
	// SupportsManaged is true when the adapter can launch the agent in
	// managed mode with structured lifecycle events.
	SupportsManaged bool `json:"supports_managed"`
	// SupportsPrompt is true when the adapter can accept a prompt without a
	// human typing into the terminal.
	SupportsPrompt bool `json:"supports_prompt"`
	// SupportsInterrupt is true when the adapter can interrupt an
	// in-progress turn.
	SupportsInterrupt bool `json:"supports_interrupt"`
	// SupportsResume is true when the adapter can resume a native session
	// by ID after a restart or reattachment.
	SupportsResume bool `json:"supports_resume"`
}

// LaunchOptions configures a new agent session.
type LaunchOptions struct {
	// Mode selects interactive or managed launch. The adapter must reject
	// modes it does not support.
	Mode Mode
	// Directory is the working directory the agent process runs in.
	Directory string
	// Columns and Rows size the PTY for interactive mode. Ignored in
	// managed mode.
	Columns int
	Rows    int
	// ResumeSessionID, when set, asks the adapter to resume a prior native
	// session instead of starting a new one. Adapters that do not support
	// resume must return an error.
	ResumeSessionID string
}

// LifecycleEvent is a structured signal an adapter emits as the agent moves
// between states or needs attention.
type LifecycleEvent struct {
	State     State
	Reason    string
	Data      map[string]string
	Timestamp time.Time
}

// Session is a running or resumable agent session produced by an adapter.
type Session interface {
	// ID returns the Orkestar-assigned session identifier.
	ID() string
	// NativeSessionID returns the adapter's own session identifier, if any,
	// for resume support. It returns an empty string when the adapter has
	// no native concept of a resumable session.
	NativeSessionID() string
	// State returns the current lifecycle state.
	State() State
	// Prompt sends a prompt to the agent. Adapters without
	// Capabilities.SupportsPrompt must return an error.
	Prompt(ctx context.Context, text string) error
	// Interrupt asks the agent to stop its current turn. Adapters without
	// Capabilities.SupportsInterrupt must return an error.
	Interrupt(ctx context.Context) error
	// Events returns a channel of lifecycle events for this session. The
	// channel is closed when the session stops.
	Events() <-chan LifecycleEvent
	// Close releases resources associated with the session. It does not
	// necessarily stop the underlying agent process; interactive sessions
	// backed by a PTY outlive client detachment.
	Close() error
}

// ResponsiveSession is an optional extension of Session for adapters that
// can return the agent's reply text for a prompt, rather than only
// delivering it and reporting lifecycle status. Callers that need
// structured output — such as a reviewer-agent verdict — should type-assert
// for this rather than assuming every Session supports it: PTY-backed
// interactive sessions generally cannot, since their output is terminal
// bytes, not a discrete reply.
type ResponsiveSession interface {
	Session
	// PromptForResponse sends text to the agent and returns its reply.
	PromptForResponse(ctx context.Context, text string) (string, error)
}

// ProcessSession is an optional extension of Session for interactive,
// PTY-backed sessions. The daemon type-asserts for this after Launch so it
// can bridge the session into the same terminal buffer/subscriber/input
// machinery used by plain terminal sessions, giving callers a real
// attachable terminal instead of a second, parallel notion of "output".
type ProcessSession interface {
	Session
	// Process returns the underlying PTY process.
	Process() *pty.Process
}

// Adapter launches and describes a specific agent integration.
type Adapter interface {
	// Capabilities describes what this adapter supports.
	Capabilities() Capabilities
	// Launch starts a new agent session or resumes one per LaunchOptions.
	Launch(ctx context.Context, options LaunchOptions) (Session, error)
}
