package pty

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/charmbracelet/x/xpty"
)

type StartOptions struct {
	Command   string
	Arguments []string
	Directory string
	Env       []string
	Columns   int
	Rows      int
}

type Process struct {
	pty    xpty.Pty
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}

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

	pseudoterminal, err := xpty.NewPty(options.Columns, options.Rows)
	if err != nil {
		return nil, fmt.Errorf("create PTY: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	command := exec.Command(options.Command, options.Arguments...)
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

	process := &Process{
		pty:    pseudoterminal,
		cmd:    command,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go process.wait(ctx)
	return process, nil
}

func (p *Process) Read(data []byte) (int, error) {
	return p.pty.Read(data)
}

func (p *Process) Write(data []byte) (int, error) {
	return p.pty.Write(data)
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
	p.cancel()
	if err := p.pty.Close(); err != nil {
		return fmt.Errorf("close PTY: %w", err)
	}
	return nil
}

func (p *Process) wait(ctx context.Context) {
	err := xpty.WaitProcess(ctx, p.cmd)
	p.waitMu.Lock()
	p.waitErr = err
	p.waitMu.Unlock()
	close(p.done)
	_ = p.pty.Close()
}
