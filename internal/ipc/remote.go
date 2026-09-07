package ipc

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
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
	client := NewClientWithDialer("ssh "+remote.Host, func(ctx context.Context, _ time.Duration) (net.Conn, error) {
		return dialSSH(ctx, remote.Host, executable)
	})
	client.remote = true
	return client
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
//
// Deliberately not exec.CommandContext. The context here belongs to the dial,
// and every caller cancels it on return — the terminal stream's five-second
// handshake context, a snapshot call's two-second one. Tying the session to it
// kills ssh the moment the call that opened it returns, which would leave a
// remote pane dead before its first frame and make pooling impossible. A
// connection's lifetime is the connection's, and Close ends it.
func dialSSH(ctx context.Context, host, executable string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return startSSHCommand(exec.Command("ssh", sshArguments(host, executable)...), host)
}

// net.Pipe supplies real, independent read/write deadlines on all platforms.
// Closing either end releases the bridge and reaps the subprocess.
func startSSHCommand(command *exec.Cmd, host string) (net.Conn, error) {
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	diagnostics := &sshDiagnostics{}
	command.Stderr = diagnostics
	command.WaitDelay = time.Second
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("run ssh: %w", err)
	}
	local, bridge := net.Pipe()
	c := &sshConn{Conn: local, command: command, bridge: bridge, stdin: stdin, stdout: stdout, host: host, diagnostics: diagnostics, done: make(chan struct{})}
	go func() {
		_, _ = io.Copy(stdin, bridge)
		_ = stdin.Close()
	}()
	go func() {
		_, _ = io.Copy(bridge, stdout)
		// The child's stdout is drained before Wait closes its pipe.
		_ = stdin.Close()
		_ = command.Wait()
		_ = bridge.Close()
		close(c.done)
	}()
	return c, nil
}

type sshConn struct {
	net.Conn
	command     *exec.Cmd
	bridge      net.Conn
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	host        string
	diagnostics *sshDiagnostics
	once        sync.Once
	done        chan struct{}
}

func (c *sshConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	if err == io.EOF && n == 0 {
		if reason := c.diagnostics.String(); reason != "" {
			return n, fmt.Errorf("%s: %s", c.host, reason)
		}
	}
	return n, err
}
func (c *sshConn) Close() error {
	c.once.Do(func() {
		_ = c.Conn.Close()
		_ = c.bridge.Close()
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		_ = c.command.Process.Kill()
	})
	<-c.done
	return nil
}
func (c *sshConn) LocalAddr() net.Addr  { return sshAddr(c.host) }
func (c *sshConn) RemoteAddr() net.Addr { return sshAddr(c.host) }

type sshDiagnostics struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *sshDiagnostics) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := 8192 - b.text.Len()
	if remaining > 0 {
		_, _ = b.text.Write(p[:min(n, remaining)])
	}
	return n, nil
}
func (b *sshDiagnostics) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.text.String())
}

type sshAddr string

func (a sshAddr) Network() string { return "ssh" }
func (a sshAddr) String() string  { return string(a) }

// SSHArguments exposes the command line for tests in other packages.
func SSHArguments(host, executable string) []string { return sshArguments(host, executable) }
