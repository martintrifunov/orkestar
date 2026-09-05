package pty

import (
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

	pseudoterminal, err := xpty.NewPty(options.Columns, options.Rows)
	if err != nil {
		return nil, fmt.Errorf("create PTY: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	command := exec.Command(options.Command, options.Arguments...)
	configureCommand(command)
	command.Dir = options.Directory
	if options.Env == nil {
		command.Env = os.Environ()
	} else {
		command.Env = options.Env
	}
	if err := pseudoterminal.Start(command); err != nil {
		cancel()
		pseudoterminal.Close()
		return nil, fmt.Errorf("start %q in PTY: %w", options.Command, err)
	}

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

func (p *Process) Read(data []byte) (int, error) {
	return p.io.Read(data)
}

func (p *Process) Write(data []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.io.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return p.io.Write(data)
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

func (p *Process) wait(ctx context.Context) {
	err := xpty.WaitProcess(ctx, p.cmd)
	finishPTY(p.pty)
	p.waitMu.Lock()
	if p.stopping.Load() {
		err = nil
	}
	p.waitErr = err
	p.waitMu.Unlock()
	close(p.done)
}
