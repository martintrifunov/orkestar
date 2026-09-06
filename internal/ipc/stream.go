package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

type Stream struct {
	connection net.Conn
	decoder    *json.Decoder
	encoder    *json.Encoder
	writeMu    sync.Mutex
	timeout    time.Duration
}

func (c *Client) OpenStream(ctx context.Context, method string, params, result any) (*Stream, error) {
	held, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	connection := held.conn
	// Context cancellation must also interrupt the handshake after dialing.
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = connection.SetDeadline(deadline)

	request := Request{
		ID:      fmt.Sprintf("req_%d", requestSequence.Add(1)),
		Version: Version,
		Method:  method,
	}
	if params != nil {
		request.Params, err = json.Marshal(params)
		if err != nil {
			connection.Close()
			return nil, fmt.Errorf("marshal %s params: %w", method, err)
		}
	}

	// The dialled conversation's reader is the only one on this connection.
	// A second would race it for buffered bytes.
	stream := &Stream{
		connection: connection,
		decoder:    held.decoder,
		encoder:    held.encoder,
		timeout:    c.timeout,
	}
	if err := stream.encoder.Encode(request); err != nil {
		connection.Close()
		return nil, fmt.Errorf("send %s request: %w", method, err)
	}

	var response Response
	if err := stream.decoder.Decode(&response); err != nil {
		connection.Close()
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
	if response.Version != Version {
		connection.Close()
		return nil, fmt.Errorf("unsupported daemon protocol version %d", response.Version)
	}
	if response.ID != request.ID {
		connection.Close()
		return nil, fmt.Errorf("mismatched response ID %q", response.ID)
	}
	if response.Error != nil {
		connection.Close()
		return nil, &RemoteError{Code: response.Error.Code, Message: response.Error.Message}
	}
	if result != nil && len(response.Result) > 0 {
		if err := json.Unmarshal(response.Result, result); err != nil {
			connection.Close()
			return nil, fmt.Errorf("decode %s result: %w", method, err)
		}
	}
	if !stop() {
		connection.Close()
		return nil, fmt.Errorf("open %s stream: %w", method, ctx.Err())
	}
	_ = connection.SetDeadline(time.Time{})
	return stream, nil
}

func (s *Stream) Send(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.connection.SetWriteDeadline(time.Now().Add(s.timeout))
	if err := s.encoder.Encode(value); err != nil {
		return fmt.Errorf("send stream message: %w", err)
	}
	return nil
}

func (s *Stream) Receive(value any) error {
	if err := s.decoder.Decode(value); err != nil {
		return fmt.Errorf("receive stream message: %w", err)
	}
	return nil
}

func (s *Stream) Close() error {
	return s.connection.Close()
}
