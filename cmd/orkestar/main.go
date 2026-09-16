package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/agent/codex"
	"github.com/martintrifunov/orkestar/internal/agent/cursor"
	"github.com/martintrifunov/orkestar/internal/agent/grok"
	"github.com/martintrifunov/orkestar/internal/agent/manifest"
	"github.com/martintrifunov/orkestar/internal/agent/opencode"
	"github.com/martintrifunov/orkestar/internal/attach"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/daemonclient"
	"github.com/martintrifunov/orkestar/internal/ipc"
	orkestarmcp "github.com/martintrifunov/orkestar/internal/mcp"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
	"github.com/martintrifunov/orkestar/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "orkestar: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version" || args[0] == "-v") {
		fmt.Println("orkestar " + version)
		return nil
	}
	// --session picks which daemon everything after it talks to, so it is read
	// before the verbs rather than by each of them.
	session := ""
	if len(args) >= 2 && args[0] == "--session" {
		session, args = args[1], args[2:]
	}
	paths, err := runtimepath.ResolveSession(session)
	if err != nil {
		return err
	}

	// --remote runs the interface here and the daemon there. It is checked
	// before the verbs because it changes which daemon every one of them
	// talks to, not what they do.
	if len(args) >= 1 && args[0] == "--remote" {
		if len(args) < 2 || len(args) > 3 {
			return errors.New("usage: orkestar --remote <[user@]host> [directory-on-that-host]")
		}
		remoteDirectory := ""
		if len(args) == 3 {
			remoteDirectory = args[2]
		}
		return runRemoteTUI(args[1], remoteDirectory)
	}

	if len(args) == 0 {
		return runTUI(paths)
	}

	switch args[0] {
	case "hook":
		return runHook()
	case "agent":
		return runAgent(paths, args[1:])
	case "daemon":
		if len(args) != 2 {
			return errors.New("usage: orkestar daemon serve|stop|proxy")
		}
		switch args[1] {
		case "serve":
			return serveDaemon(paths)
		case "stop":
			return stopDaemon(paths)
		case "proxy":
			return proxyDaemon(paths)
		default:
			return errors.New("usage: orkestar daemon serve|stop|proxy")
		}
	case "reset":
		return runReset(paths, args[1:])
	case "status":
		return printStatus(paths)
	case "workspace":
		return runWorkspace(paths, args[1:])
	case "terminal":
		return runTerminal(paths, args[1:])
	case "task":
		return runTask(paths, args[1:])
	case "template":
		return runTemplate(paths, args[1:])
	case "mcp":
		return runMCP(paths, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runTUI(paths runtimepath.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	return tui.Run(ipc.NewClient(paths.Socket), directory, filepath.Join(paths.Directory, "layout.json"))
}

// runRemoteTUI attaches to a daemon on another machine. The interface runs
// here, which is the point: notifications, the clipboard and the terminal all
// belong to the machine the person is sitting at, while the agents keep
// running on the one that has the work.
func runRemoteTUI(host, directory string) error {
	remote, err := ipc.ParseRemote(host)
	if err != nil {
		return err
	}
	client := ipc.NewRemoteClient(remote)
	defer client.Close()

	// Reached before anything else so a version mismatch, an unreachable host
	// or a missing binary is reported plainly rather than as a failed snapshot
	// once the interface is already up.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var status map[string]string
	if err := client.Call(ctx, "system.ping", nil, &status); err != nil {
		return err
	}
	// Compatibility is the protocol generation, not the build: two builds on
	// the same protocol interoperate, so only a real protocol difference is
	// refused, and the message names it.
	if compatible, detail := protocolCompatible(status); !compatible {
		return fmt.Errorf(
			"this is orkestar %s (protocol %d) and %s speaks %s; the two cannot talk until one side is upgraded",
			version, ipc.Version, remote.Host, detail)
	}

	// The interface needs a directory to create workspaces, tasks and shells
	// in, and it is the remote's directory rather than this machine's. With
	// none given, an existing workspace supplies it; with no workspaces
	// either, say so rather than starting an interface where every create key
	// fails on an empty path.
	if directory == "" {
		var snapshot daemon.Snapshot
		if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
			return err
		}
		if len(snapshot.Workspaces) == 0 {
			return fmt.Errorf(
				"%s has no workspaces yet, so there is nothing to work in; pass a directory on that machine: orkestar --remote %s <directory>",
				remote.Host, remote.Host)
		}
		directory = snapshot.Workspaces[0].Directory
	}
	// A remote session keeps its own layout: this client would attach to
	// terminals on another machine, and a remembered layout from a local one
	// would point at IDs that mean nothing there.
	return tui.Run(client, directory, "")
}

func runTerminal(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: orkestar terminal start <workspace-id> -- <command> [args...] | attach <terminal-id> | read <terminal-id> [--lines N] | send <terminal-id> [--enter] <text> | wait <terminal-id> --contains <text> [--timeout N] | stop <terminal-id> | remove <terminal-id>")
	}

	switch args[0] {
	case "start":
		if len(args) < 3 {
			return errors.New("usage: orkestar terminal start <workspace-id> -- <command> [args...]")
		}
		commandIndex := 2
		if args[commandIndex] == "--" {
			commandIndex++
		}
		if commandIndex >= len(args) {
			return errors.New("terminal command is required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var terminal daemon.Terminal
		if err := ipc.NewClient(paths.Socket).Call(ctx, "terminal.start", map[string]any{
			"workspace_id": args[1],
			"command":      args[commandIndex:],
			"columns":      80,
			"rows":         24,
		}, &terminal); err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", terminal.ID, terminal.State, terminal.Command[0])
		return nil
	case "read":
		if len(args) < 2 {
			return errors.New("usage: orkestar terminal read <terminal-id> [--lines N]")
		}
		terminalID := args[1]
		lineCount := 0
		for index := 2; index < len(args); index++ {
			if args[index] != "--lines" || index+1 >= len(args) {
				return errors.New("usage: orkestar terminal read <terminal-id> [--lines N]")
			}
			count, err := strconv.Atoi(args[index+1])
			if err != nil {
				return fmt.Errorf("--lines needs a number: %w", err)
			}
			lineCount = count
			index++
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result struct {
			Text    string `json:"text"`
			Columns int    `json:"columns"`
			Rows    int    `json:"rows"`
		}
		if err := ipc.NewClient(paths.Socket).Call(ctx, "terminal.read", map[string]any{
			"terminal_id": terminalID,
			"lines":       lineCount,
		}, &result); err != nil {
			return err
		}
		fmt.Println(result.Text)
		return nil
	case "send":
		if len(args) < 3 {
			return errors.New("usage: orkestar terminal send <terminal-id> [--enter] <text>")
		}
		terminalID := args[1]
		enter := false
		var words []string
		for _, argument := range args[2:] {
			if argument == "--enter" {
				enter = true
				continue
			}
			words = append(words, argument)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result map[string]string
		if err := ipc.NewClient(paths.Socket).Call(ctx, "terminal.send", map[string]any{
			"terminal_id": terminalID,
			"text":        strings.Join(words, " "),
			"enter":       enter,
		}, &result); err != nil {
			return err
		}
		fmt.Printf("%s sent\n", terminalID)
		return nil
	case "wait":
		if len(args) < 2 {
			return errors.New("usage: orkestar terminal wait <terminal-id> --contains <text> [--timeout N]")
		}
		terminalID := args[1]
		contains := ""
		timeoutSeconds := 0
		for index := 2; index < len(args); index++ {
			switch args[index] {
			case "--contains":
				if index+1 >= len(args) {
					return errors.New("--contains needs a value")
				}
				contains = args[index+1]
				index++
			case "--timeout":
				if index+1 >= len(args) {
					return errors.New("--timeout needs a number")
				}
				seconds, err := strconv.Atoi(args[index+1])
				if err != nil {
					return fmt.Errorf("--timeout needs a number: %w", err)
				}
				timeoutSeconds = seconds
				index++
			default:
				return fmt.Errorf("unknown option %q", args[index])
			}
		}
		if contains == "" {
			return errors.New("terminal wait needs --contains <text>")
		}
		// The daemon owns the wait, so the call's deadline has to outlast it.
		callTimeout := 5*time.Minute + 15*time.Second
		if timeoutSeconds > 0 {
			callTimeout = time.Duration(timeoutSeconds)*time.Second + 15*time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var result struct {
			Text  string `json:"text"`
			State string `json:"state"`
		}
		if err := ipc.NewClient(paths.Socket).Call(ctx, "terminal.wait", map[string]any{
			"terminal_id":     terminalID,
			"contains":        contains,
			"timeout_seconds": timeoutSeconds,
		}, &result); err != nil {
			return err
		}
		fmt.Println(result.Text)
		return nil
	case "stop", "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: orkestar terminal %s <terminal-id>", args[0])
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result any
		if err := ipc.NewClient(paths.Socket).Call(ctx, "terminal."+args[0], map[string]any{"terminal_id": args[1]}, &result); err != nil {
			return err
		}
		past := map[string]string{"stop": "stopped", "remove": "removed"}[args[0]]
		fmt.Printf("%s %s\n", args[1], past)
		return nil
	case "attach":
		if len(args) != 2 {
			return errors.New("usage: orkestar terminal attach <terminal-id>")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return attach.Terminal(ctx, ipc.NewClient(paths.Socket), args[1], os.Stdin, os.Stdout)
	default:
		return fmt.Errorf("unknown terminal command %q", args[0])
	}
}

// proxyDaemon joins this process's stdin and stdout to the local daemon
// socket. It is the far half of remote attachment: a client elsewhere runs
// `ssh <host> orkestar daemon proxy` and talks to the daemon through it.
//
// The daemon keeps its owner-only socket and never listens on a network. What
// crosses machines is an ssh session, authenticated by ssh, which is why
// Orkestar has no credential of its own to get wrong.
func proxyDaemon(paths runtimepath.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Started on demand, so attaching to a machine where nothing is running
	// works the same way it does locally.
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}

	conn, err := ipc.Dial(context.Background(), paths.Socket, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	// Either direction ending ends the session: a closed stdin means the
	// client hung up, and a closed socket means the daemon did.
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		// Tell the daemon no more requests are coming, so it can finish the
		// one it has rather than waiting on a reader that is gone.
		if closer, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, conn)
		done <- err
	}()
	if err := <-done; err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func runMCP(paths runtimepath.Paths, args []string) error {
	if len(args) == 1 && args[0] == "instructions" {
		// The same text an MCP client is sent on connect, for an agent that
		// drives Orkestar through the CLI and never sees it.
		_, err := fmt.Println(orkestarmcp.Instructions())
		return err
	}
	if len(args) != 1 || args[0] != "serve" {
		return errors.New("usage: orkestar mcp serve|instructions")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}

	server := orkestarmcp.NewServer(ipc.NewClient(paths.Socket))
	runCtx, runCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer runCancel()
	return server.Run(runCtx, &sdk.StdioTransport{})
}

func serveDaemon(paths runtimepath.Paths) error {
	if err := runtimepath.Ensure(paths); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := daemon.NewServer(paths.Socket)
	server.SetVersion(version)
	server.SetAutoResume(autoResumeEnabled())
	server.SetPaneHistory(paneHistoryEnabled())
	adapters, err := buildAdapters()
	if err != nil {
		fmt.Fprintf(os.Stderr, "orkestar: %v\n", err)
	}
	for _, adapter := range adapters {
		server.RegisterAdapter(adapter)
	}
	// A reload rebuilds the whole set, so a changed or removed manifest takes
	// effect without restarting the daemon.
	server.SetAdapterLoader(buildAdapters)
	// OpenCode is the reviewer adapter because its managed mode returns a
	// structured reply; Claude Code's interactive PTY adapter has no
	// discrete response to parse a verdict from.
	server.SetReviewerAdapter("opencode")
	return server.Serve(ctx)
}

// buildAdapters returns the whole adapter set: the built-ins, then the user's
// declarative manifests, which may take a built-in's name. A bad manifest is
// reported and skipped rather than stopping the daemon.
func buildAdapters() ([]agent.Adapter, error) {
	adapters := []agent.Adapter{
		claude.New(""),
		codex.New(""),
		opencode.New("", "", nil),
		cursor.New(""),
		grok.New(""),
	}
	directory, err := runtimepath.AgentManifestDirectory()
	if err != nil {
		return adapters, err
	}
	manifests, manifestErr := manifest.LoadDir(directory)
	if manifestErr != nil {
		fmt.Fprintf(os.Stderr, "orkestar: %v\n", manifestErr)
	}
	for _, descriptor := range manifests {
		adapter, err := manifest.New(descriptor)
		if err != nil {
			fmt.Fprintf(os.Stderr, "orkestar: %v\n", err)
			continue
		}
		adapters = append(adapters, adapter)
	}
	return adapters, nil
}

// autoResumeEnabled reports whether the daemon may relaunch the agent sessions
// that were running when it last stopped, once the first client connects.
// Enabled by default; ORKESTAR_AUTO_RESUME=0 turns it off.
func autoResumeEnabled() bool {
	return os.Getenv("ORKESTAR_AUTO_RESUME") != "0"
}

// paneHistoryEnabled reports whether terminal text should survive a daemon
// restart. Off by default: pane output can contain secrets.
func paneHistoryEnabled() bool {
	return os.Getenv("ORKESTAR_PANE_HISTORY") == "1"
}

// protocolCompatible reports whether this client can talk to the daemon whose
// ping status it has. A daemon that advertises a protocol generation must
// match; one too old to advertise it is judged by build version, which is all
// it had.
func protocolCompatible(status map[string]string) (bool, string) {
	if remoteProtocol := status["protocol"]; remoteProtocol != "" {
		if remoteProtocol != strconv.Itoa(ipc.Version) {
			return false, "protocol " + remoteProtocol
		}
		return true, ""
	}
	if remoteVersion := status["version"]; remoteVersion != "" && remoteVersion != version {
		return false, "version " + remoteVersion
	}
	return true, ""
}

func stopDaemon(paths runtimepath.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result map[string]string
	if err := ipc.NewClient(paths.Socket).Call(ctx, "system.shutdown", nil, &result); err != nil {
		return err
	}
	fmt.Println(result["status"])
	return nil
}

func printStatus(paths runtimepath.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var result struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := ipc.NewClient(paths.Socket).Call(ctx, "system.ping", nil, &result); err != nil {
		return fmt.Errorf("daemon is not available at %s: %w", paths.Socket, err)
	}
	if result.Version == "" {
		result.Version = "unversioned"
	}
	fmt.Printf("daemon %s, version %s; client %s (%s)\n", result.Status, result.Version, version, paths.Socket)
	return nil
}

func runWorkspace(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: orkestar workspace create [directory]")
	}
	if len(args) > 2 {
		return errors.New("usage: orkestar workspace create [directory]")
	}

	directory := "."
	if len(args) == 2 {
		directory = args[1]
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("resolve workspace directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var workspace daemon.Workspace
	if err := ipc.NewClient(paths.Socket).Call(ctx, "workspace.create", map[string]string{
		"directory": directory,
	}, &workspace); err != nil {
		return err
	}
	fmt.Printf("%s\t%s\t%s\n", workspace.ID, workspace.Name, workspace.Directory)
	return nil
}

func printUsage() {
	fmt.Print(`Orkestar coordinates persistent coding-agent sessions.

Usage:
  orkestar
  orkestar --session <name> [command...]
  orkestar --remote <[user@]host> [directory-on-that-host]
  orkestar daemon serve
  orkestar daemon stop
  orkestar daemon proxy
  orkestar status
  orkestar reset [--yes]
  orkestar workspace create [directory]
  orkestar terminal start <workspace-id> -- <command> [args...]
  orkestar terminal attach <terminal-id>
  orkestar terminal stop <terminal-id>
  orkestar terminal remove <terminal-id>
  orkestar agent list
  orkestar agent launch <workspace-id> <claude-code|codex|opencode> [--task=<task-id>]
  orkestar agent resume <agent-id>
  orkestar agent stop <agent-id>
  orkestar agent remove <agent-id>
  orkestar agent interrupt <agent-id>
  orkestar task create <workspace-id> <title> [--depends-on id1,id2] [--no-review]
  orkestar task list [workspace-id]
  orkestar task edit <task-id> [--title=…] [--description=…] [--depends-on=id1,id2]
  orkestar task status <task-id> <pending|in_progress|done|cancelled>
  orkestar task assign <task-id> <agent-id>
  orkestar task worktree create <task-id> [branch]
  orkestar task worktree remove <task-id>
  orkestar task wait <task-id> [done|finished|startable] [--timeout=300]
  orkestar task diff <task-id>
  orkestar template list <workspace-id>
  orkestar template apply <workspace-id> <name> [--start]
  orkestar mcp serve
  orkestar mcp instructions
  orkestar help

Detach from an attached terminal with ctrl+b q.
`)
}

// runReset clears everything the daemon holds. It previews by default: a full
// reset is easy to ask for by accident and impossible to undo, so the state is
// only discarded once the caller has seen what will go and passed --yes.
func runReset(paths runtimepath.Paths, args []string) error {
	confirmed := false
	for _, argument := range args {
		switch argument {
		case "--yes", "-y":
			confirmed = true
		default:
			return fmt.Errorf("usage: orkestar reset [--yes]")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	client := ipc.NewClient(paths.Socket)

	if !confirmed {
		var state daemon.Snapshot
		if err := client.Call(ctx, "system.snapshot", nil, &state); err != nil {
			return err
		}
		running := 0
		for _, terminal := range state.Terminals {
			if terminal.State == "running" {
				running++
			}
		}
		active := 0
		var worktrees []string
		for _, agent := range state.Agents {
			switch agent.State {
			case "stopped", "crashed", "interrupted":
			default:
				active++
			}
		}
		for _, task := range state.Tasks {
			if task.WorktreePath != "" {
				worktrees = append(worktrees, task.WorktreePath)
			}
		}
		if len(state.Terminals)+len(state.Agents)+len(state.Tasks)+len(state.Workspaces) == 0 {
			fmt.Println("Nothing to reset; the daemon is already empty.")
			return nil
		}
		fmt.Println("A reset stops and clears everything the daemon is holding:")
		fmt.Printf("  %-11s %d", "sessions", len(state.Terminals))
		if running > 0 {
			fmt.Printf("  (%d still running)", running)
		}
		fmt.Println()
		fmt.Printf("  %-11s %d", "agents", len(state.Agents))
		if active > 0 {
			fmt.Printf("  (%d still running)", active)
		}
		fmt.Println()
		for _, line := range []struct {
			label string
			count int
		}{
			{"tasks", len(state.Tasks)},
			{"artifacts", len(state.Artifacts)},
			{"workspaces", len(state.Workspaces)},
			{"leases", len(state.Leases)},
		} {
			fmt.Printf("  %-11s %d\n", line.label, line.count)
		}
		if len(worktrees) > 0 {
			fmt.Println("\nTask worktrees stay on disk and are not deleted:")
			for _, path := range worktrees {
				fmt.Println("  " + path)
			}
		}
		fmt.Println("\nNothing has changed. Run 'orkestar reset --yes' to go ahead.")
		return nil
	}

	// Always retire the running binary first. In particular, an older daemon
	// need not understand system.reset; only the fresh binary receives it.
	if err := daemonclient.Stop(ctx, paths.Socket); err != nil {
		return err
	}
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	var summary daemon.ResetSummary
	resetErr := client.Call(ctx, "system.reset", map[string]any{"confirm": true}, &summary)
	stopErr := daemonclient.Stop(ctx, paths.Socket)
	if err := errors.Join(resetErr, stopErr); err != nil {
		return err
	}
	fmt.Println("Reset complete. Daemon stopped. Cleared:")
	for _, line := range []struct {
		label string
		count int
	}{
		{"sessions", summary.Terminals},
		{"agents", summary.Agents},
		{"tasks", summary.Tasks},
		{"artifacts", summary.Artifacts},
		{"workspaces", summary.Workspaces},
		{"leases", summary.Leases},
	} {
		fmt.Printf("  %-11s %d\n", line.label, line.count)
	}
	if len(summary.Worktrees) > 0 {
		fmt.Println("\nThese task worktrees were left on disk:")
		for _, path := range summary.Worktrees {
			fmt.Println("  " + path)
		}
		fmt.Println("Remove one with 'git worktree remove <path>' if you no longer need it.")
	}
	return nil
}
