package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var requestSequence atomic.Uint64

type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// maxIdleConnections bounds what a client keeps around. The TUI asks for a
// snapshot every second and a handful of calls can be in flight at once; more
// idle connections than that is a leak rather than a saving.
const maxIdleConnections = 4

// conversation is a connection kept for reuse together with the reader that
// owns whatever it has already buffered. Pooling the connection alone would
// drop bytes a previous read had pulled in but not consumed.
type conversation struct {
	conn    net.Conn
	decoder *json.Decoder
	encoder *json.Encoder
}

type Client struct {
	socketPath string
	timeout    time.Duration
	// dialer opens one connection to the daemon. It is a field so a remote
	// client can reach a daemon over ssh without the rest of the client
	// knowing anything about transports.
	dialer func(ctx context.Context, timeout time.Duration) (net.Conn, error)
	// describe names where this client is pointed, for errors that would
	// otherwise say only "connect to daemon" while talking to another machine.
	describe string

	mu     sync.Mutex
	idle   []*conversation
	closed bool
	remote bool
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath, timeout: 2 * time.Second}
}

// NewClientWithDialer returns a client that reaches the daemon however dial
// says. describe names the far end for errors, which otherwise say only
// "connect to daemon" while talking to another machine.
func NewClientWithDialer(describe string, dial func(context.Context, time.Duration) (net.Conn, error)) *Client {
	return &Client{timeout: 2 * time.Second, dialer: dial, describe: describe}
}

// Close releases pooled connections. A client is usually as long-lived as the
// process, so this is for tests and for anything that makes clients in a loop.
func (c *Client) Close() error {
	c.mu.Lock()
	idle := c.idle
	c.idle = nil
	c.closed = true
	c.mu.Unlock()
	for _, held := range idle {
		_ = held.conn.Close()
	}
	return nil
}

// take returns a pooled connection, or nil when there is none.
func (c *Client) take() *conversation {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.idle) == 0 {
		return nil
	}
	held := c.idle[len(c.idle)-1]
	c.idle = c.idle[:len(c.idle)-1]
	return held
}

// keep returns a connection to the pool, but only one that completed a whole
// request and response. Anything that failed partway may have a reply still
// coming, and handing that to the next caller would answer it with someone
// else's result.
func (c *Client) keep(held *conversation) {
	if err := held.conn.SetDeadline(time.Time{}); err != nil {
		_ = held.conn.Close()
		return
	}
	c.mu.Lock()
	if c.closed || len(c.idle) >= maxIdleConnections {
		c.mu.Unlock()
		_ = held.conn.Close()
		return
	}
	c.idle = append(c.idle, held)
	c.mu.Unlock()
}

func (c *Client) dial(ctx context.Context) (*conversation, error) {
	open := c.dialer
	if open == nil {
		open = func(ctx context.Context, timeout time.Duration) (net.Conn, error) {
			return Dial(ctx, c.socketPath, timeout)
		}
	}
	conn, err := open(ctx, c.timeout)
	if err != nil {
		where := "daemon"
		if c.describe != "" {
			where = c.describe
		}
		return nil, fmt.Errorf("connect to %s: %w", where, err)
	}
	return &conversation{
		conn:    conn,
		decoder: json.NewDecoder(bufio.NewReader(conn)),
		encoder: json.NewEncoder(conn),
	}, nil
}

// Call sends one request and reads its reply, reusing a connection when one is
// free. The daemon has always read requests from a connection in a loop; only
// the client insisted on a fresh one each time, which costs little over a Unix
// socket and a great deal over anything else.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	request := Request{
		ID:      fmt.Sprintf("req_%d", requestSequence.Add(1)),
		Version: Version,
		Method:  method,
	}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal %s params: %w", method, err)
		}
		request.Params = encoded
	}

	// A pooled connection may have been closed at the far end since it was
	// last used — a daemon restart, most obviously. A send that writes zero bytes proves
	// the daemon never saw the request, so only that one is safe to retry on a
	// fresh connection. A reply that fails to arrive is not: the request may
	// well have been carried out, and repeating a mutation is worse than
	// reporting an error.
	held := c.take()
	if held == nil {
		fresh, err := c.dial(ctx)
		if err != nil {
			return err
		}
		// Nothing was pooled, so there is no stale connection to retry past.
		return unwrapSend(c.converse(ctx, fresh, request, method, result))
	}

	err := c.converse(ctx, held, request, method, result)
	var failedToSend *sendFailed
	if !errors.As(err, &failedToSend) || ctx.Err() != nil {
		return err
	}
	fresh, dialErr := c.dial(ctx)
	if dialErr != nil {
		return dialErr
	}
	return unwrapSend(c.converse(ctx, fresh, request, method, result))
}

// sendFailed marks the one failure that is safe to retry: the request never
// reached the daemon.
type sendFailed struct{ err error }

func (e *sendFailed) Error() string { return e.err.Error() }
func (e *sendFailed) Unwrap() error { return e.err }

// unwrapSend strips the retry marker from an error that is being returned
// rather than retried, so callers see the failure and not the bookkeeping.
func unwrapSend(err error) error {
	var failedToSend *sendFailed
	if errors.As(err, &failedToSend) {
		return failedToSend.err
	}
	return err
}

func (c *Client) converse(ctx context.Context, held *conversation, request Request, method string, result any) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := held.conn.SetDeadline(deadline); err != nil {
			_ = held.conn.Close()
			return fmt.Errorf("set IPC deadline: %w", err)
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = held.conn.Close() })
	defer stop()
	writer := &countedWriter{Writer: held.conn}
	if err := json.NewEncoder(writer).Encode(request); err != nil {
		_ = held.conn.Close()
		failure := fmt.Errorf("send %s request: %w", method, err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if writer.n == 0 {
			return &sendFailed{failure}
		}
		return failure
	}

	var response Response
	if err := held.decoder.Decode(&response); err != nil {
		_ = held.conn.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read %s response: %w", method, err)
	}
	if response.Version != Version {
		_ = held.conn.Close()
		return fmt.Errorf("unsupported daemon protocol version %d", response.Version)
	}
	if response.ID != request.ID {
		// The connection is out of step with itself and cannot be trusted for
		// anything further.
		_ = held.conn.Close()
		return fmt.Errorf("mismatched response ID %q", response.ID)
	}
	if !stop() {
		_ = held.conn.Close()
		return ctx.Err()
	}
	c.keep(held)

	if response.Error != nil {
		return &RemoteError{Code: response.Error.Code, Message: response.Error.Message}
	}
	if result == nil || len(response.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

func IsUnavailable(err error) bool {
	var networkError *net.OpError
	return errors.As(err, &networkError)
}

// IdleConnections reports how many connections the client is holding, so a
// test can prove the pool stays bounded in a process that runs for days.
func (c *Client) IdleConnections() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.idle)
}

// IsRemote reports whether filesystem paths belong to another machine.
func (c *Client) IsRemote() bool { return c != nil && c.remote }

type countedWriter struct {
	io.Writer
	n int
}

func (w *countedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.n += n
	if n != len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}
