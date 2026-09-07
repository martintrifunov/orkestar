package pty

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/xpty"
)

type terminalIO interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	SetWriteDeadline(time.Time) error
}

type StartOptions struct {
	Command   string
	Arguments []string
	Directory string
	Env       []string
	Columns   int
	Rows      int
}

type Process struct {
	io        terminalIO
	writeMu   sync.Mutex
	pty       xpty.Pty
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	stopping  atomic.Bool
	// interruptedAt is when Ctrl-C was last written to this process, in Unix
	// nanoseconds. A process that then dies of that interrupt stopped because
	// it was asked to, and wait must not report it as a crash.
	interruptedAt atomic.Int64

	waitMu  sync.RWMutex
	waitErr error
}

func Start(options StartOptions) (*Process, error) {
	if options.Command == "" {
		return nil, fmt.Errorf("start PTY: command is required")
	}
	if options.Columns <= 0 {
		options.Columns = 80
	}
	if options.Rows <= 0 {
		options.Rows = 24
	}
	if options.Columns > 500 || options.Rows > 200 {
		return nil, fmt.Errorf("start PTY: size exceeds 500 columns or 200 rows")
	}

	pseudoterminal, command, err := startWithRetry(options)
	if err != nil {
		return nil, fmt.Errorf("start %q in PTY: %w", options.Command, err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	ioFile, err := prepareIO(pseudoterminal)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = pseudoterminal.Close()
		cancel()
		return nil, fmt.Errorf("configure PTY I/O: %w", err)
	}

	process := &Process{
		pty:    pseudoterminal,
		io:     ioFile,
		cmd:    command,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go process.wait(ctx)
	return process, nil
}

// textFileBusyRetries bounds how many times startWithRetry retries a launch
// that fails with ETXTBSY: something else briefly held the executable open
// for writing right as it was about to run. Seen against a fixture written
// and executed in the same instant; plausible in production too, against a
// binary a package manager is mid-upgrade on.
const textFileBusyRetries = 5

const textFileBusyDelay = 20 * time.Millisecond

// startWithRetry starts command in a fresh PTY, retrying with a new PTY and
// command on ETXTBSY. A failed exec.Cmd cannot be reused for a second Start,
// so each attempt gets its own.
func startWithRetry(options StartOptions) (xpty.Pty, *exec.Cmd, error) {
	var lastErr error
	for attempt := 0; attempt < textFileBusyRetries; attempt++ {
		pseudoterminal, err := xpty.NewPty(options.Columns, options.Rows)
		if err != nil {
			return nil, nil, fmt.Errorf("create PTY: %w", err)
		}
		command := exec.Command(options.Command, options.Arguments...)
		configureCommand(command)
		command.Dir = options.Directory
		if options.Env == nil {
			command.Env = os.Environ()
		} else {
			command.Env = options.Env
		}
		if err := pseudoterminal.Start(command); err != nil {
			pseudoterminal.Close()
			lastErr = err
			if !isTextFileBusy(err) {
				return nil, nil, err
			}
			time.Sleep(textFileBusyDelay)
			continue
		}
		return pseudoterminal, command, nil
	}
	return nil, nil, lastErr
}

func (p *Process) Read(data []byte) (int, error) {
	return p.io.Read(data)
}

// interruptGrace is how long after a Ctrl-C an exit caused by SIGINT is still
// read as that interrupt. Death by the signal we just sent is immediate; a
// process that survives and dies of SIGINT much later was killed by something
// else, and calling that a clean stop would hide a real failure.
const interruptGrace = 5 * time.Second

func (p *Process) Write(data []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.io.SetWriteDeadline(time.Now().Add(2 * time.Second))
	// Ctrl-C reaches a process two ways: Interrupt, and a user typing it into
	// an attached pane, which arrives here as an ordinary input byte. Both are
	// the same request, so record it here rather than in Interrupt alone.
	if bytes.IndexByte(data, 0x03) >= 0 {
		p.interruptedAt.Store(time.Now().UnixNano())
	}
	return p.io.Write(data)
}

// Interrupt sends Ctrl-C the way a user at an interactive terminal would.
// Most agent CLIs treat it as "stop this turn" and carry on; one that exits
// instead has still stopped because it was told to. Write records the request,
// so typing the same byte into an attached pane counts too.
func (p *Process) Interrupt() error {
	_, err := p.Write([]byte{0x03})
	return err
}

func (p *Process) Resize(columns, rows int) error {
	if columns <= 0 || rows <= 0 {
		return fmt.Errorf("resize PTY: dimensions must be positive")
	}
	if err := p.pty.Resize(columns, rows); err != nil {
		return fmt.Errorf("resize PTY to %dx%d: %w", columns, rows, err)
	}
	return nil
}

func (p *Process) Done() <-chan struct{} {
	return p.done
}

func (p *Process) WaitError() error {
	<-p.done
	p.waitMu.RLock()
	defer p.waitMu.RUnlock()
	return p.waitErr
}

func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		defer p.cancel()
		defer p.pty.Close()
		defer p.io.Close()
		p.stopping.Store(true)
		select {
		case <-p.done:
			return
		default:
		}
		// The PTY child is a session leader. Signal its group so subprocesses also
		// stop on an explicit daemon shutdown, then bound the grace period.
		stopProcessGroup(p.cmd.Process, false)
		select {
		case <-p.done:
		case <-time.After(time.Second):
			stopProcessGroup(p.cmd.Process, true)
			_ = p.cmd.Process.Kill()
		}
		_ = p.pty.Close()
		p.cancel()
	})
	return nil
}

// recentlyInterrupted reports whether Ctrl-C was written recently enough that
// a SIGINT death is attributable to it.
func (p *Process) recentlyInterrupted() bool {
	at := p.interruptedAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) < interruptGrace
}

func (p *Process) wait(ctx context.Context) {
	err := xpty.WaitProcess(ctx, p.cmd)
	finishPTY(p.pty)
	p.waitMu.Lock()
	// Close sets stopping, but an interrupt reaches the process first and can
	// kill it before Close is ever called: on Linux the shell dies of SIGINT
	// where macOS's survives it. Both are the process ending because it was
	// asked to.
	if p.stopping.Load() || (p.recentlyInterrupted() && interruptedExit(err)) {
		err = nil
	}
	p.waitErr = err
	p.waitMu.Unlock()
	close(p.done)
}
