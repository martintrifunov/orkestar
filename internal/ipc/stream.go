package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

type Stream struct {
	connection net.Conn
	decoder    *json.Decoder
	encoder    *json.Encoder
	writeMu    sync.Mutex
}

func (c *Client) OpenStream(ctx context.Context, method string, params, result any) (*Stream, error) {
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}

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

	stream := &Stream{
		connection: connection,
		decoder:    json.NewDecoder(bufio.NewReader(connection)),
		encoder:    json.NewEncoder(connection),
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
	return stream, nil
}

func (s *Stream) Send(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
