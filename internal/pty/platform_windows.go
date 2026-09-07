package pty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/windows"
)

// controlCExit is what a console process reports when it ends on Ctrl-C:
// STATUS_CONTROL_C_EXIT. Windows has no SIGINT wait status, so this is the
// same fact the Unix build reads out of the signal.
const controlCExit = 0xC000013A

// isTextFileBusy reports whether err is ETXTBSY. CreateProcess has no
// equivalent failure mode, so this never fires on Windows.
func isTextFileBusy(err error) bool { return false }

// interruptedExit reports whether err says the process ended on the Ctrl-C we
// sent it, which is a stop rather than a crash.
func interruptedExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return uint32(exitErr.ExitCode()) == controlCExit
}

func configureCommand(cmd *exec.Cmd) {
	ext := strings.ToLower(filepath.Ext(cmd.Path))
	if ext != ".cmd" && ext != ".bat" {
		return
	}
	// CreateProcess cannot launch batch files, including npm's agent shims.
	// Escape both the C argv layer and cmd's metacharacters. This follows
	// cross-spawn's Windows command handling:
	// https://github.com/moxystudio/node-cross-spawn/blob/master/lib/util/escape.js
	command := escapeCmdMeta(cmd.Path)
	double := strings.Contains(strings.ToLower(filepath.ToSlash(cmd.Path)), "/node_modules/.bin/")
	for _, arg := range cmd.Args[1:] {
		quoted := quoteWindowsArg(arg)
		quoted = escapeCmdMeta(quoted)
		if double {
			quoted = escapeCmdMeta(quoted)
		}
		command += " " + quoted
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	*cmd = *exec.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(shell) + ` /d /s /c "` + command + `"`}
}

func escapeCmdMeta(s string) string {
	var out strings.Builder
	for _, r := range s {
		if strings.ContainsRune("()[]%!^\"`<>&|;, *?", r) {
			out.WriteByte('^')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func quoteWindowsArg(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			out.WriteString(strings.Repeat("\\", 2*slashes+1))
		} else {
			out.WriteString(strings.Repeat("\\", slashes))
		}
		slashes = 0
		out.WriteRune(r)
	}
	out.WriteString(strings.Repeat("\\", 2*slashes))
	out.WriteByte('"')
	return out.String()
}

func stopProcessGroup(process *os.Process, force bool) {
	// taskkill /T includes descendants, unlike os.Process.Kill on Windows.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"/PID", strconv.Itoa(process.Pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	_ = exec.CommandContext(ctx, "taskkill.exe", args...).Run()
}

// ConPTY uses synchronous pipes. Cancel the blocked writer's OS thread when
// its deadline expires; wait for cancellation to end before returning the thread
// to Go so it cannot cancel an unrelated operation.
var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

type consoleIO struct {
	xpty.Pty
	deadline time.Time
}

func (c *consoleIO) SetWriteDeadline(t time.Time) error { c.deadline = t; return nil }
func (c *consoleIO) Write(data []byte) (int, error) {
	deadline := c.deadline
	if deadline.IsZero() {
		// No deadline set: write without arming the canceller.
		return c.Pty.Write(data)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(thread)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		}
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			_, _, _ = cancelSynchronousIO.Call(uintptr(thread))
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	n, err := c.Pty.Write(data)
	close(done)
	<-stopped
	if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
		err = os.ErrDeadlineExceeded
	}
	return n, err
}

// The pseudoconsole owns its pipe handles; Process closes it separately.
func (c *consoleIO) Close() error              { return nil }
func prepareIO(p xpty.Pty) (terminalIO, error) { return &consoleIO{Pty: p}, nil }

// Closing after process exit lets the concurrent output reader drain ConPTY's
// final frame and receive EOF. Unix already gets EOF from its closed slave.
func finishPTY(p xpty.Pty) { _ = p.Close() }
