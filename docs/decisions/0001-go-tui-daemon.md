# ADR 0001: Go daemon with a TUI-first client

- Status: accepted
- Date: 2026-09-03

## Context

Orkestar must keep interactive coding agents alive independently of its user
interface, support local IPC and concurrent streams, and remain comfortable for
terminal-oriented developers. A web interface is not desired for the initial
product. A standalone desktop application would add packaging and UI complexity
before the core runtime is validated.

## Decision

Implement the runtime, CLI, and primary TUI in Go. Ship them from one executable
with explicit modes/subcommands. Run long-lived work in a background daemon and
connect disposable clients over local IPC.

Use Bubble Tea v2 for the TUI. Keep Bubble Tea, PTY, terminal emulator, storage,
and transport dependencies behind Orkestar-owned package boundaries. Target
macOS and Linux first while preserving a platform-neutral PTY interface for a
future Windows ConPTY implementation.

Do not build a web or desktop client during the initial milestones. Future
engine-native panels or graphical clients must remain thin clients of the same
daemon.

## Consequences

Positive:

- Fast development and straightforward concurrency.
- One approachable implementation language for the initial product.
- Strong TUI libraries and simple binary distribution.
- Natural HTTP, JSON, MCP, and local IPC integration.
- UI failures cannot terminate managed work when process ownership is correct.

Tradeoffs:

- Cross-platform PTYs require careful platform-specific implementations.
- Current Go terminal-emulation options include experimental packages.
- Claude and OpenCode publish their richest SDKs in other languages, so Go will
  integrate through stable process, JSONL, HTTP, event, hook, and plugin
  boundaries; optional sidecars may be introduced only when justified.
- Rich image and visual-diff workflows may require terminal-specific protocols
  or opening an external viewer.

## Revisit when

- Windows becomes a release requirement.
- A required agent capability is unavailable through documented external
  protocols.
- Proven workflows require a graphical surface that engine-native panels and
  terminal image support cannot provide.
