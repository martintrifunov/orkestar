package ipc

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"time"
)

// Remote attachment runs the client here and the daemon there. The transport
// is ssh and so is the authentication: the user's existing keys, agent,
// ~/.ssh/config, jump hosts and second factors all apply, and Orkestar invents
// no credential of its own. The daemon keeps its owner-only local socket and
// never listens on a network.
//
// Each connection is `ssh <host> orkestar daemon proxy`, whose stdio is the
// far end's socket. Connection reuse in the client is what makes that
// affordable, and ssh's own multiplexing makes the connections that are still
// opened cheap.

// controlPathTemplate lets ssh share one authenticated connection across the
// processes a session opens. Without it every pooled connection would pay a
// full handshake, which on a slow link is most of a second each.
const controlPathTemplate = "~/.ssh/orkestar-%C"

// Remote names a daemon reached over ssh.
type Remote struct {
	// Host is anything ssh accepts: a hostname, user@host, or a Host alias
	// from ~/.ssh/config.
	Host string
	// Executable is the orkestar binary on the far side, for the case where it
	// is not on a non-interactive PATH.
	Executable string
}

// ParseRemote reads the --remote form. It deliberately accepts only what ssh
// itself would, and passes it through untouched rather than parsing a user or
// a port back out: ssh already knows how to read its own config.
func ParseRemote(value string) (Remote, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return Remote{}, fmt.Errorf("a remote needs a host")
	}
	if strings.HasPrefix(host, "-") {
		// Otherwise a host could smuggle in ssh options.
		return Remote{}, fmt.Errorf("remote host %q cannot start with a dash", host)
	}
	return Remote{Host: host, Executable: "orkestar"}, nil
}

// NewRemoteClient returns a client whose connections are ssh sessions to the
// daemon on that host.
func NewRemoteClient(remote Remote) *Client {
	executable := remote.Executable
	if executable == "" {
		executable = "orkestar"
	}
	return NewClientWithDialer("ssh "+remote.Host, func(ctx context.Context, _ time.Duration) (net.Conn, error) {
		return dialSSH(ctx, remote.Host, executable)
	})
}

// sshArguments is the command line each connection runs. It is separate so a
// test can check it without an sshd: the options here decide whether every
// pooled connection pays a fresh handshake, and the -- decides whether a host
// can pass ssh options of its own.
func sshArguments(host, executable string) []string {
	return []string{
		"-o", "BatchMode=yes",
		// Share one authenticated connection between sessions, and hold it
		// briefly after the last, so a burst of calls pays for one handshake.
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + controlPathTemplate,
		"-o", "ControlPersist=60",
		"--", host, executable, "daemon", "proxy",
	}
}

// dialSSH starts one ssh session and presents its stdio as a connection.
func dialSSH(ctx context.Context, host, executable string) (net.Conn, error) {
	command := exec.CommandContext(ctx, "ssh", sshArguments(host, executable)...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", host, err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", host, err)
	}
	// ssh reports a refused host or a missing key here, and it is the only
	// useful thing to say when the far end never answers.
	var diagnostics strings.Builder
	command.Stderr = &diagnostics
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("run ssh: %w", err)
	}
	return &sshConn{command: command, stdin: stdin, stdout: stdout, host: host, diagnostics: &diagnostics}, nil
}

// sshConn presents an ssh session's stdio as a net.Conn. Only what the IPC
// client uses is real: reads, writes, close and a deadline.
type sshConn struct {
	command     *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	host        string
	diagnostics *strings.Builder
	deadline    time.Time
}

func (c *sshConn) Read(data []byte) (int, error) {
	n, err := c.stdout.Read(data)
	if err != nil && n == 0 {
		// A dead session says nothing on its own; ssh's stderr is where the
		// reason is, and without it the user sees only "EOF".
		if reason := strings.TrimSpace(c.diagnostics.String()); reason != "" {
			return n, fmt.Errorf("%s: %s", c.host, reason)
		}
	}
	return n, err
}

func (c *sshConn) Write(data []byte) (int, error) { return c.stdin.Write(data) }

func (c *sshConn) Close() error {
	_ = c.stdin.Close()
	_ = c.stdout.Close()
	if c.command.Process != nil {
		_ = c.command.Process.Kill()
	}
	_, _ = c.command.Process.Wait()
	return nil
}

func (c *sshConn) LocalAddr() net.Addr  { return sshAddr(c.host) }
func (c *sshConn) RemoteAddr() net.Addr { return sshAddr(c.host) }

// SetDeadline is accepted and not enforced. A pipe has no deadline to set, and
// the client's calls already carry a context that closes the session when it
// ends, which is what a deadline would have been for.
func (c *sshConn) SetDeadline(t time.Time) error      { c.deadline = t; return nil }
func (c *sshConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *sshConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

type sshAddr string

func (a sshAddr) Network() string { return "ssh" }
func (a sshAddr) String() string  { return string(a) }

// SSHArguments exposes the command line for tests in other packages.
func SSHArguments(host, executable string) []string { return sshArguments(host, executable) }
