package ipc

import (
	"encoding/json"
	"fmt"
)

const Version = 1

type Request struct {
	ID      string          `json:"id"`
	Version int             `json:"version"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID      string          `json:"id"`
	Version int             `json:"version"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Event struct {
	Version int             `json:"version"`
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewResponse(id string, result any) (Response, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return Response{}, fmt.Errorf("marshal response result: %w", err)
	}

	return Response{ID: id, Version: Version, Result: payload}, nil
}

func NewErrorResponse(id, code, message string) Response {
	return Response{
		ID:      id,
		Version: Version,
		Error:   &Error{Code: code, Message: message},
	}
}
