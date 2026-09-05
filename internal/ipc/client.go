package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

type Client struct {
	socketPath string
	timeout    time.Duration
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath, timeout: 2 * time.Second}
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	conn, err := Dial(ctx, c.socketPath, c.timeout)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set IPC deadline: %w", err)
		}
	}

	request := Request{
		ID:      fmt.Sprintf("req_%d", requestSequence.Add(1)),
		Version: Version,
		Method:  method,
	}
	if params != nil {
		request.Params, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal %s params: %w", method, err)
		}
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return fmt.Errorf("send %s request: %w", method, err)
	}

	var response Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&response); err != nil {
		return fmt.Errorf("read %s response: %w", method, err)
	}
	if response.Version != Version {
		return fmt.Errorf("unsupported daemon protocol version %d", response.Version)
	}
	if response.ID != request.ID {
		return fmt.Errorf("mismatched response ID %q", response.ID)
	}
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
